package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// The egress wall (ADR-0028). Workstations sit on an internal network the
// daemon gives no route off; one proxy container bridges it to the outside
// and forwards only allowlisted hosts. Both are fleet infrastructure: ensured
// at every wake, before a workstation is provisioned or started, and never
// removed with a loop.
const (
	egressPort = "3128"

	// egressGatewayHost is the alias the proxy reaches the operator's machine
	// by, and so the name a loop's hub traffic is addressed to (ADR-0026).
	egressGatewayHost = "host.docker.internal"

	// egressSpecLabel records which configuration a proxy container was
	// created with.
	egressSpecLabel = "spool.egress.spec"

	// egressFileFormat names what the proxy reads from its file. It is in
	// the spec so a proxy from an image that reads another file is replaced
	// rather than kept: the fleet's entries moved into the file with #542,
	// and an older proxy would refuse every one of them, and the file lost
	// its loops' entries with #626, along with the flag that names it.
	egressFileFormat = "file:fleet"
)

// The wall is named after the image that enforces it: one daemon can carry a
// production fleet and a test suite's fleet at once, and they must not end up
// sharing a network or reusing each other's proxy container.
//
//	spool-egress         -> network spool-egress, container spool-egress-proxy
//	spool-egress-itest   -> network spool-egress-itest, ...-itest-proxy
func egressPrefix(image string) string {
	name := image
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	if colon := strings.Index(name, ":"); colon >= 0 {
		name = name[:colon]
	}
	return name
}

func (rt *Runtime) egressNetwork() string   { return egressPrefix(rt.egressImage) }
func (rt *Runtime) egressContainer() string { return egressPrefix(rt.egressImage) + "-proxy" }

// loopNetwork is the internal network a loop's workstation sits on alone
// with the proxy. A workstation keeps the hub's MCP token, which crosses
// its network to the proxy as plain HTTP, so no other workstation may be
// on that network to read it.
//
//	spool-egress, loop abc   -> network spool-egress-loop-abc
func (rt *Runtime) loopNetwork(loopID string) string {
	return rt.egressNetwork() + "-loop-" + loopID
}

// loopNetworkWallLabel marks a loop network with the wall it belongs to,
// so a recreated proxy finds every network it must rejoin.
const loopNetworkWallLabel = "spool.egress.wall"

// egressProxyURL is what the exec env points every client at. The name
// resolves on the internal network through docker's own DNS.
func (rt *Runtime) egressProxyURL() string {
	return "http://" + rt.egressContainer() + ":" + egressPort
}

// ensureEgress makes the internal network and the proxy container exist and
// run. Idempotent — it is called on every wake, so a proxy an
// operator removed comes back with the next wake.
func (rt *Runtime) ensureEgress(ctx context.Context) error {
	if !rt.egressEnabled() {
		return nil
	}
	rt.egressMu.Lock()
	defer rt.egressMu.Unlock()
	if err := rt.ensureEgressNetwork(ctx); err != nil {
		return err
	}
	if err := rt.ensureEgressProxy(ctx); err != nil {
		return err
	}
	return rt.catchUpEgressFile(ctx)
}

// ensureLoopEgress puts the wall up and the loop's own network beside it,
// with the proxy on that network.
func (rt *Runtime) ensureLoopEgress(ctx context.Context, loopID string) error {
	if !rt.egressEnabled() {
		return nil
	}
	if err := rt.ensureEgress(ctx); err != nil {
		return err
	}
	rt.egressMu.Lock()
	defer rt.egressMu.Unlock()
	network := rt.loopNetwork(loopID)
	err := rt.ensureInternalNetwork(ctx, network,
		"spool.egress=loop-network",
		loopNetworkWallLabel+"="+rt.egressNetwork(),
		"spool.loop.id="+loopID)
	if err != nil {
		return withPoolRemedy(err)
	}
	return rt.connectProxy(ctx, network)
}

// connectProxy attaches the proxy to network, which is all a workstation
// there has to reach it by: the proxy's name resolves on every network it
// is on.
func (rt *Runtime) connectProxy(ctx context.Context, network string) error {
	if _, err := rt.command(ctx, startTimeout, "network", "connect", network, rt.egressContainer()); err != nil && !alreadyConnected(err) {
		return err
	}
	return nil
}

// reconnectLoopNetworks attaches a newly provisioned proxy to every loop
// network of its wall. A running workstation would otherwise lose its way
// out until its own next wake.
func (rt *Runtime) reconnectLoopNetworks(ctx context.Context) error {
	out, err := rt.command(ctx, queryTimeout, "network", "ls", "--format", "{{.Name}}",
		"--filter", "label="+loopNetworkWallLabel+"="+rt.egressNetwork())
	if err != nil {
		return err
	}
	for _, network := range strings.Fields(string(out)) {
		if err := rt.connectProxy(ctx, network); err != nil {
			return err
		}
	}
	return nil
}

// removeLoopNetwork takes a destroyed loop's network down. The proxy is
// detached first, since docker refuses to remove a network with members.
func (rt *Runtime) removeLoopNetwork(ctx context.Context, loopID string) error {
	if !rt.egressEnabled() {
		return nil
	}
	rt.egressMu.Lock()
	defer rt.egressMu.Unlock()
	network := rt.loopNetwork(loopID)
	if _, err := rt.command(ctx, startTimeout, "network", "disconnect", "--force", network, rt.egressContainer()); err != nil && !networkGone(err) && !notFound(err) && !notConnected(err) {
		return err
	}
	if _, err := rt.command(ctx, startTimeout, "network", "rm", network); err != nil && !networkGone(err) {
		return err
	}
	return nil
}

func (rt *Runtime) ensureEgressNetwork(ctx context.Context) error {
	return rt.ensureInternalNetwork(ctx, rt.egressNetwork(), "spool.egress=network")
}

// ensureInternalNetwork makes network exist, --internal and with labels.
// A network already there under the name is refused unless it is
// internal: one made without --internal, by hand or by another tool,
// would give everything on it a route out that skips the proxy.
func (rt *Runtime) ensureInternalNetwork(ctx context.Context, network string, labels ...string) error {
	out, err := rt.command(ctx, queryTimeout, "network", "inspect", "--format", "{{.Internal}}", network)
	if err == nil {
		if strings.TrimSpace(string(out)) != "true" {
			return fmt.Errorf("docker network %s is not internal, so it would give a workstation a way out past the egress proxy: "+
				"remove it (docker network rm %s) and the hub recreates it", network, network)
		}
		return nil
	}
	argv := []string{"network", "create", "--internal"}
	for _, label := range labels {
		argv = append(argv, "--label", label)
	}
	if _, err := rt.command(ctx, startTimeout, append(argv, network)...); err != nil && !alreadyExists(err) {
		return err
	}
	return nil
}

func (rt *Runtime) ensureEgressProxy(ctx context.Context) error {
	imageID, err := rt.imageID(ctx, rt.egressImage)
	if err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	state, err := rt.inspectState(ctx, rt.egressContainer())
	if err == nil && imageID != "" {
		// A proxy running an older configuration — a rebuilt image (#715),
		// or a hub that has moved to another port — is the wrong wall, and
		// its run arguments are fixed at creation. Replacing it is cheap: its
		// one state, the operator's extra hosts, is the hub's to copy back
		// in, and a workstation reconnects to the name. With its image gone
		// there is nothing to replace it from, so it is kept.
		current, readErr := rt.egressSpec(ctx)
		if readErr != nil {
			return readErr
		}
		if current != rt.egressSpecHash(imageID) {
			slog.Default().Info("egress proxy configuration changed", "image", rt.egressImage, "id", shortID(imageID))
			if _, rmErr := rt.command(ctx, startTimeout, "rm", "--force", rt.egressContainer()); rmErr != nil && !notFound(rmErr) {
				return rmErr
			}
			return rt.provisionEgressProxyWithFleet(ctx, imageID)
		}
	}
	switch {
	case errors.Is(err, errNotFound):
		return rt.provisionEgressProxyWithFleet(ctx, imageID)
	case err != nil:
		return err
	case state.Paused:
		_, err := rt.command(ctx, startTimeout, "unpause", rt.egressContainer())
		return err
	case state.Running:
		return nil
	default:
		_, err := rt.command(ctx, startTimeout, "start", rt.egressContainer())
		return err
	}
}

// provisionEgressProxyWithFleet provisions a proxy and gives it back the
// operator's extra hosts, which a new container doesn't have.
func (rt *Runtime) provisionEgressProxyWithFleet(ctx context.Context, imageID string) error {
	if err := rt.provisionEgressProxy(ctx, imageID); err != nil {
		return err
	}
	return rt.rewriteEgressFile(ctx)
}

func (rt *Runtime) provisionEgressProxy(ctx context.Context, imageID string) error {
	if _, err := rt.command(ctx, runTimeout, rt.egressRunArgv(imageID)...); err != nil && !nameInUse(err) {
		return withImageRemedy(err, rt.egressImage, false)
	}
	// The run above put the proxy on the internal network only; a second
	// network is a separate call, and it is the one that gives the proxy —
	// and nothing else — a way out.
	if _, err := rt.command(ctx, startTimeout, "network", "connect", "bridge", rt.egressContainer()); err != nil {
		if !alreadyConnected(err) {
			return err
		}
	}
	if err := rt.reconnectLoopNetworks(ctx); err != nil {
		return err
	}
	_, err := rt.command(ctx, startTimeout, "start", rt.egressContainer())
	return err
}

// egressRunArgv provisions the proxy: on the internal network, restarting
// like a workstation does, with the host gateway alias it needs to reach the
// hub's MCP endpoint on the operator's machine (ADR-0026). The spec label
// records imageID, the ID the image has as it is run.
//
// The hub is the one allowlist entry the proxy cannot carry compiled in: it
// lives on the gateway on whatever port the operator gave --mcp-listen, and
// it is permitted on that port alone — otherwise an allowlisted gateway would
// be a tunnel to every port of the operator's own machine, the API port it
// serves the control room on included (ADR-0028, #238).
func (rt *Runtime) egressRunArgv(imageID string) []string {
	argv := []string{
		"run", "--pull=never", "--detach", "--init",
		"--name", rt.egressContainer(),
		"--restart", "unless-stopped",
		"--network", rt.egressNetwork(),
		"--add-host", egressGatewayHost + ":host-gateway",
		"--label", "spool.egress=proxy",
		"--label", egressSpecLabel + "=" + rt.egressSpecHash(imageID),
		rt.egressImage,
		"--listen", ":" + egressPort,
		"--fleet-file", fleetEgressFile,
	}
	if allow := rt.egressEntries(); len(allow) > 0 {
		argv = append(argv, "--allow", strings.Join(allow, ","))
	}
	return argv
}

// egressEntries are the allowlist entries the proxy is started with on top
// of its built-in defaults: the hub's MCP port alone. The hub's API port is
// deliberately not among them, and the operator's extra hosts go in the
// proxy's file instead, so changing them recreates nothing (#542).
func (rt *Runtime) egressEntries() []string {
	if gateway := rt.EgressGateway(); gateway != "" {
		return []string{gateway}
	}
	return nil
}

// EgressGateway is the allowlist entry the hub is reached by, the gateway
// on the MCP port, or "" when the wall is down.
func (rt *Runtime) EgressGateway() string {
	if !rt.egressEnabled() || rt.mcpPort == "" {
		return ""
	}
	return egressGatewayHost + ":" + rt.mcpPort
}

// egressSpecHash identifies the configuration a running proxy was created
// with, so a changed one is noticed and replaced rather than silently kept.
// imageID is the ID the proxy image has now: a rebuild under the same tag
// is a changed configuration too (#715).
func (rt *Runtime) egressSpecHash(imageID string) string {
	spec := append([]string{rt.egressImage, imageID, egressPort, egressFileFormat}, rt.egressEntries()...)
	sum := sha256.Sum256([]byte(strings.Join(spec, "\x00")))
	return hex.EncodeToString(sum[:6])
}

func (rt *Runtime) egressSpec(ctx context.Context) (string, error) {
	out, err := rt.command(ctx, queryTimeout,
		"inspect", "--type", "container", "--format", "{{json .Config.Labels}}", rt.egressContainer())
	if err != nil {
		if notFound(err) {
			return "", nil
		}
		return "", err
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &labels); err != nil {
		return "", err
	}
	return labels[egressSpecLabel], nil
}

func (rt *Runtime) egressEnabled() bool { return rt.egressImage != "" }

// EgressEnabled reports whether workstation egress runs through the
// allowlist proxy at all (ADR-0028): false when the hub was started without
// an egress image, and every docker loop reaches any host.
func (rt *Runtime) EgressEnabled() bool { return rt.egressEnabled() }

// egressEnv points every client inside the workstation at the proxy. These
// are not credentials, so unlike the loop's own variables they cross in argv
// as KEY=VALUE — nothing here is worth hiding from host `ps`, and a value in
// argv is one fewer thing the exec client's environment has to carry.
//
// NO_PROXY keeps a loop's own local servers direct: a dev server it starts on
// localhost is inside the wall already and has no business going out and back.
func (rt *Runtime) egressEnv() []string {
	if !rt.egressEnabled() {
		return nil
	}
	url := rt.egressProxyURL()
	var argv []string
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		argv = append(argv, "--env", name+"="+url)
	}
	return append(argv, "--env", "NO_PROXY=localhost,127.0.0.1,::1", "--env", "no_proxy=localhost,127.0.0.1,::1")
}

// networkArgs puts a container behind the wall, on network: a workstation
// on its loop network, a login check on the wall's own. A runtime with no
// egress image configured provisions on the default bridge — the
// pre-ADR-0028 posture, which is what the bare-adjacent "no image built
// yet" case gets.
func (rt *Runtime) networkArgs(network string) []string {
	if !rt.egressEnabled() {
		// the loop reaches the hub's MCP endpoint through the host gateway
		// (ADR-0026); with the wall up, the proxy holds that alias instead
		return []string{"--add-host", "host.docker.internal:host-gateway"}
	}
	return []string{"--network", network}
}

// EgressWall reports the fleet's egress posture for the wirer to log at boot:
// the proxy image (empty when egress is left open) and whether that image
// exists locally. A missing image is a warning rather than a fatal, so the hub
// still starts and the operator is told what to build — but a contained loop
// will not wake until it is there, because provisioning it is the first thing
// a workstation needs.
func (rt *Runtime) EgressWall(ctx context.Context) (image string, ready bool) {
	if !rt.egressEnabled() {
		return "", false
	}
	_, err := rt.command(ctx, queryTimeout, "image", "inspect", rt.egressImage)
	return rt.egressImage, err == nil
}

// alreadyExists and alreadyConnected are the races two orchestrators (or two
// concurrent wakes) can lose harmlessly: the resource is there, which is all
// the caller wanted.
func alreadyExists(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

func alreadyConnected(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "already exists in network")
}
