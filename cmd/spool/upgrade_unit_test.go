package main

import (
	"strings"
	"testing"
)

const testUnitShow = "ExecStart={ path=/usr/local/bin/spool ; argv[]=/usr/local/bin/spool --data-dir /var/lib/spool --listen=127.0.0.1:9090 --egress-image spool-egress-x ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }\nUser=spool\n"

// The upgrade reads its paths from the unit, either flag form, with each
// default the hub itself would use (ADR-0057).
func TestParseUnitReadsPathsFromExecStart(t *testing.T) {
	unit, err := parseUnit("spool", testUnitShow)
	if err != nil {
		t.Fatal(err)
	}
	want := hubUnit{
		Name: "spool", Bin: "/usr/local/bin/spool", HookBin: "/usr/local/bin/spool-hook",
		DataDir: "/var/lib/spool", Listen: "127.0.0.1:9090",
		WorkstationImage: "spool-workstation", EgressImage: "spool-egress-x", User: "spool",
	}
	if unit != want {
		t.Fatalf("unit = %+v\nwant %+v", unit, want)
	}

	unit, err = parseUnit("spool", strings.Replace(testUnitShow, "--listen=127.0.0.1:9090", "--hook-bin=/opt/hook", 1))
	if err != nil || unit.HookBin != "/opt/hook" || unit.Listen != "127.0.0.1:8080" {
		t.Fatalf("with --hook-bin: %+v, %v", unit, err)
	}
}

// A unit the upgrade can't read for sure is a refusal, never a guess.
func TestParseUnitRefusesWhatItCantRead(t *testing.T) {
	for name, show := range map[string]string{
		"no unit":        "ExecStart=\nUser=\n",
		"two ExecStarts": testUnitShow + testUnitShow,
		"no data dir":    strings.Replace(testUnitShow, "--data-dir /var/lib/spool ", "", 1),
		"relative data":  strings.Replace(testUnitShow, "/var/lib/spool", "spool-data", 1),
		"dangling flag":  strings.Replace(testUnitShow, " ; ignore_errors", " --listen ; ignore_errors", 1),
	} {
		if _, err := parseUnit("spool", show); err == nil {
			t.Errorf("%s: parsed; want a refusal", name)
		}
	}
}
