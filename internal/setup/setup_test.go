package setup

import (
	"strings"
	"testing"
)

func TestEmbeddedFiles(t *testing.T) {
	if !strings.Contains(string(udevRule), `ATTRS{idVendor}=="1532"`) {
		t.Fatal("udev rule not embedded")
	}
	// Install puts the binary where the unit starts it from.
	if !strings.Contains(string(unitFile), "ExecStart=%h/.local/bin/razerctl daemon") {
		t.Fatal("unit ExecStart does not match binPath")
	}
}
