package browser

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBrowserPortableCLISettingsReachRouteConsumer(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	handled, err := runPortableBrowser([]string{"route", "--json", "--home", home}, &out)
	if !handled || err != nil {
		t.Fatalf("default route: handled=%t err=%v", handled, err)
	}
	var route Route
	if err := json.Unmarshal(out.Bytes(), &route); err != nil || route.Backend != "playwright" {
		t.Fatalf("default route output: %s, %v", out.String(), err)
	}
	out.Reset()
	handled, err = runPortableBrowser([]string{"settings", "--enable", "--work", "work_browser_cli", "--json", "--home", home}, &out)
	if !handled || err != nil {
		t.Fatalf("enable: handled=%t err=%v output=%s", handled, err, out.String())
	}
	var changed SettingsChange
	if err := json.Unmarshal(out.Bytes(), &changed); err != nil || !changed.Changed || changed.ReceiptID == "" {
		t.Fatalf("enable output: %s, %v", out.String(), err)
	}
	out.Reset()
	handled, err = runPortableBrowser([]string{"route", "--json", "--home", home}, &out)
	if !handled || err != nil {
		t.Fatalf("enabled route: handled=%t err=%v", handled, err)
	}
	if err := json.Unmarshal(out.Bytes(), &route); err != nil || route.Backend != "jev" {
		t.Fatalf("enabled route output: %s, %v", out.String(), err)
	}
	out.Reset()
	handled, err = runPortableBrowser([]string{"route", "--json", "--home", home, "--profile", "profile-a"}, &out)
	if !handled || err != nil {
		t.Fatalf("profile route: handled=%t err=%v", handled, err)
	}
	if err := json.Unmarshal(out.Bytes(), &route); err != nil || route.Backend != "playwright" {
		t.Fatalf("profile route output: %s, %v", out.String(), err)
	}
}

func TestBrowserPortableCLIRejectsMalformedFlagsBeforeMutation(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"settings", "--enable", "--disable", "--work", "work_browser_cli", "--home", home},
		{"settings", "--enable", "--home", home},
		{"route", "--enable", "--home", home},
		{"settings", "--unknown", "--home", home},
	} {
		if handled, err := runPortableBrowser(args, &bytes.Buffer{}); !handled || err == nil {
			t.Fatalf("invalid args accepted: %v (handled=%t, err=%v)", args, handled, err)
		}
	}
	settings, err := LoadSettings(home)
	if err != nil || settings.JevEnabled {
		t.Fatalf("invalid args changed settings: %+v, %v", settings, err)
	}
}

func TestBrowserGoalProfileHandoffNeedsNoProviderBudget(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	handled, err := runPortableBrowser([]string{"goal", "--home", home, "--work", "work_browser_cli", "--url", "https://example.com/", "--goal", "Read the heading", "--expect-text", "Example Domain", "--profile", "profile-a", "--json"}, &out)
	if !handled || err != ErrRoutedToPlaywright {
		t.Fatalf("profile handoff: handled=%t err=%v", handled, err)
	}
	var result TaskResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Backend != "playwright" || result.Status != "routed" || result.Verified {
		t.Fatalf("profile handoff result: %+v, %v", result, err)
	}
}
