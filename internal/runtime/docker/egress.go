package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// The egress wall (ADR-0028). Workstations sit on an internal network the
// daemon gives no route off; one proxy container bridges it to the outside
// and forwards only allowlisted hosts. Both are fleet infrastructure: ensured
// before the first workstation is provisioned, never removed with a loop.
const (
	egressPort = "3128"

	// egressGatewayHost is the alias the proxy reaches the operator's machine
	// by, and so the name a loop's hub traffic is addressed to (ADR-0026).
	egressGatewayHost = "host.docker.internal"

	// egressSpecLabel records which configuration a proxy container was
	// created with.
	egressSpecLabel = "spool.egress.spec"
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

// egressProxyURL is what the exec env points every client at. The name
// resolves on the internal network through docker's own DNS.
func (rt *Runtime) egressProxyURL() string {
	return "http://" + rt.egressContainer() + ":" + egressPort
}

// ensureEgress makes the internal network and the proxy container exist and
// run. Idempotent — it is called before every provision, so a proxy an
// operator removed comes back with the next wake.
func (rt *Runtime) ensureEgress(ctx context.Context) error {
	if !rt.egressEnabled() {
		return nil
	}
	if err := rt.ensureEgressNetwork(ctx); err != nil {
		return err
	}
	return rt.ensureEgressProxy(ctx)
}

func (rt *Runtime) ensureEgressNetwork(ctx context.Context) error {
	if _, err := rt.command(ctx, queryTimeout, "network", "inspect", rt.egressNetwork()); err == nil {
		return nil
	}
	_, err := rt.command(ctx, startTimeout,
		"network", "create", "--internal", "--label", "spool.egress=network", rt.egressNetwork())
	if err != nil && !alreadyExists(err) {
		return err
	}
	return nil
}

func (rt *Runtime) ensureEgressProxy(ctx context.Context) error {
	state, err := rt.inspectState(ctx, rt.egressContainer())
	if err == nil {
		// A proxy running an older configuration — a different image, or a
		// hub that has moved to another port — is the wrong wall, and its run
		// arguments are fixed at creation. Replacing it is cheap: it holds no
		// state, and a workstation reconnects to the name.
		current, readErr := rt.egressSpec(ctx)
		if readErr != nil {
			return readErr
		}
		if current != rt.egressSpecHash() {
			if _, rmErr := rt.command(ctx, startTimeout, "rm", "--force", rt.egressContainer()); rmErr != nil && !notFound(rmErr) {
				return rmErr
			}
			return rt.provisionEgressProxy(ctx)
		}
	}
	switch {
	case errors.Is(err, errNotFound):
		return rt.provisionEgressProxy(ctx)
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

func (rt *Runtime) provisionEgressProxy(ctx context.Context) error {
	if _, err := rt.command(ctx, runTimeout, rt.egressRunArgv()...); err != nil && !nameInUse(err) {
		return err
	}
	// The run above put the proxy on the internal network only; a second
	// network is a separate call, and it is the one that gives the proxy —
	// and nothing else — a way out.
	if _, err := rt.command(ctx, startTimeout, "network", "connect", "bridge", rt.egressContainer()); err != nil {
		if !alreadyConnected(err) {
			return err
		}
	}
	_, err := rt.command(ctx, startTimeout, "start", rt.egressContainer())
	return err
}

// egressRunArgv provisions the proxy: on the internal network, restarting
// like a workstation does, with the host gateway alias it needs to reach the
// hub's MCP endpoint on the operator's machine (ADR-0026).
//
// The hub is the one allowlist entry the proxy cannot carry compiled in: it
// lives on the gateway on whatever port the operator gave --mcp-listen, and
// it is permitted on that port alone — otherwise an allowlisted gateway would
// be a tunnel to every port of the operator's own machine, the API port it
// serves the control room on included (ADR-0028, #238).
func (rt *Runtime) egressRunArgv() []string {
	argv := []string{
		"run", "--detach", "--init",
		"--name", rt.egressContainer(),
		"--restart", "unless-stopped",
		"--network", rt.egressNetwork(),
		"--add-host", egressGatewayHost + ":host-gateway",
		"--label", "spool.egress=proxy",
		"--label", egressSpecLabel + "=" + rt.egressSpecHash(),
		rt.egressImage,
		"--listen", ":" + egressPort,
	}
	if allow := rt.egressEntries(); len(allow) > 0 {
		argv = append(argv, "--allow", strings.Join(allow, ","))
	}
	return argv
}

// egressEntries are the allowlist entries this fleet adds to the proxy's
// built-in defaults: the hub's MCP port, plus whatever the operator
// configured. The hub's API port is deliberately not among them.
func (rt *Runtime) egressEntries() []string {
	var entries []string
	if rt.mcpPort != "" {
		entries = append(entries, egressGatewayHost+":"+rt.mcpPort)
	}
	return append(entries, rt.egressAllow...)
}

// egressSpecHash identifies the configuration a running proxy was created
// with, so a changed one is noticed and replaced rather than silently kept.
func (rt *Runtime) egressSpecHash() string {
	spec := append([]string{rt.egressImage, egressPort}, rt.egressEntries()...)
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
	proxy := []string{
		"HTTP_PROXY=" + url,
		"HTTPS_PROXY=" + url,
		"http_proxy=" + url,
		"https_proxy=" + url,
		"NO_PROXY=localhost,127.0.0.1,::1",
		"no_proxy=localhost,127.0.0.1,::1",
	}
	argv := make([]string, 0, len(proxy)*2)
	for _, kv := range proxy {
		argv = append(argv, "--env", kv)
	}
	return argv
}

// networkArgs puts a workstation behind the wall. A runtime with no egress
// image configured provisions on the default bridge — the pre-ADR-0028
// posture, which is what the bare-adjacent "no image built yet" case gets.
func (rt *Runtime) networkArgs() []string {
	if !rt.egressEnabled() {
		// the loop reaches the hub's MCP endpoint through the host gateway
		// (ADR-0026); with the wall up, the proxy holds that alias instead
		return []string{"--add-host", "host.docker.internal:host-gateway"}
	}
	return []string{"--network", rt.egressNetwork()}
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
