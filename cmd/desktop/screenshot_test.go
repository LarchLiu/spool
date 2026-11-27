package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"spool/internal/screencapture"
	"strings"
	"testing"
)

func TestScreenshotContentKeepsPerDisplayTargetsAndEmptyWindowArray(t *testing.T) {
	global := []screencapture.Window{{ID: 1, Name: "global"}}
	local := []screencapture.Window{{ID: 2, Name: "monitor-local"}}
	content := screenshotContents(&screencapture.Snapshot{Windows: global, Screens: []screencapture.Screen{{PNG: []byte{1}, Windows: local}, {PNG: []byte{2}, Windows: []screencapture.Window{}}, {PNG: []byte{3}}}})
	if len(content[0].Windows) != 1 || content[0].Windows[0].ID != 2 || len(content[1].Windows) != 0 || content[1].Windows == nil || content[2].Windows[0].ID != 1 {
		t.Fatal("display-specific targets were replaced by global coordinates")
	}
	picked := screenshotContents(&screencapture.Snapshot{Picked: true, Screens: []screencapture.Screen{{PNG: []byte{4}}}})
	data, err := json.Marshal(picked[0])
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if windows, ok := value["windows"].([]any); !ok || len(windows) != 0 {
		t.Fatalf("system-picked editor requires an empty window array, got %s", data)
	}
}

func TestScreenshotNavigationRequiresExactOriginTokenAndVisibility(t *testing.T) {
	const base = "http://127.0.0.1:8787/servers/remote/"
	const token = "23bd1659-1291-4bf9-9c30-bd6d80c84a59"
	for _, visibility := range []string{"hide", "show"} {
		request, reserved, ok := screenshotNavigation(base, "http://127.0.0.1:8787/__desktop/screenshot?token="+token+"&visibility="+visibility)
		if !reserved || !ok || request.Token != token || request.Visibility != visibility {
			t.Fatalf("valid screenshot request rejected: %+v %v %v", request, reserved, ok)
		}
	}
	for _, target := range []string{
		"https://127.0.0.1:8787/__desktop/screenshot?token=" + token + "&visibility=show",
		"http://other.test/__desktop/screenshot?token=" + token + "&visibility=show",
		"http://user@127.0.0.1:8787/__desktop/screenshot?token=" + token + "&visibility=show",
		base + "__desktop/screenshot?token=" + token + "&visibility=show",
		"http://127.0.0.1:8787/__desktop/screenshot?token=" + token,
		"http://127.0.0.1:8787/__desktop/screenshot?token=x&visibility=show",
		"http://127.0.0.1:8787/__desktop/screenshot?token=" + token + "&visibility=screen",
		"http://127.0.0.1:8787/__desktop/screenshot?token=" + token + "&visibility=show&path=/etc/passwd",
		"http://127.0.0.1:8787/__desktop/screenshot?token=" + token + "&visibility=show&visibility=hide",
		"http://127.0.0.1:8787/__desktop/screenshot?token=" + token + "&visibility=show#fragment",
		"http://127.0.0.1:8787/__desktop/%73creenshot?token=" + token + "&visibility=show",
	} {
		if _, _, ok := screenshotNavigation(base, target); ok {
			t.Errorf("unsafe screenshot request accepted: %s", target)
		}
	}
}

func TestScreenshotChoiceIsBoundToOwnedSurfaceAndSnapshot(t *testing.T) {
	const token = "selection-token"
	for _, target := range []string{
		"mygo://app/__screenshot?token=selection-token&action=ready",
		"mygo://app/__screenshot?token=selection-token&action=cancel",
		"mygo://app/__screenshot?token=selection-token&action=complete",
	} {
		if _, _, ok := screenshotChoiceNavigation(target, token); !ok {
			t.Errorf("valid choice rejected: %s", target)
		}
	}
	for _, target := range []string{
		"https://app/__screenshot?token=selection-token&action=cancel",
		"mygo://other/__screenshot?token=selection-token&action=cancel",
		"mygo://app/__screenshot?token=other-token&action=cancel",
		"mygo://app/__screenshot?token=selection-token&action=window&id=43",
		"mygo://app/__screenshot?token=selection-token&action=region&x=-1001&y=200&width=300&height=400",
		"mygo://app/__screenshot?token=selection-token&action=region&x=-800&y=200&width=0&height=400",
		"mygo://app/__screenshot?token=selection-token&action=region&x=-800&y=200&width=300&height=4000",
		"mygo://app/__screenshot?token=selection-token&action=region&x=-800&y=200&width=300&height=400&height=4",
		"mygo://app/__screenshot?token=selection-token&action=cancel&path=/etc/passwd",
		"mygo://app/__screenshot?token=selection-token&action=complete&data=anything",
		"mygo://app/__screenshot?token=selection-token&action=complete&token=other",
	} {
		if _, _, ok := screenshotChoiceNavigation(target, token); ok {
			t.Errorf("unsafe choice accepted: %s", target)
		}
	}
}

func TestScreenshotExportRequiresCompleteBoundedPNG(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 20, 10))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(encoded.Bytes())
	if got, err := decodeScreenshotExport(data, 100, 100); err != nil || !bytes.Equal(got, encoded.Bytes()) {
		t.Fatalf("valid export: %v", err)
	}
	for _, invalid := range []struct {
		data          string
		width, height int
	}{
		{"", 100, 100}, {"not base64", 100, 100}, {base64.StdEncoding.EncodeToString([]byte("not PNG")), 100, 100},
		{data, 19, 10}, {data, 20, 9},
		{base64.StdEncoding.EncodeToString(encoded.Bytes()[:encoded.Len()-16]), 100, 100},
	} {
		if _, err := decodeScreenshotExport(invalid.data, invalid.width, invalid.height); err == nil {
			t.Fatal("invalid PNG accepted")
		}
	}
	if _, err := decodeScreenshotExport(strings.Repeat("A", base64.StdEncoding.EncodedLen(4*1024*1024)+1), 100, 100); err != screencapture.ErrTooLarge {
		t.Fatalf("oversized export: %v", err)
	}
}
