package workspace

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestServerNameDefaultsAndNormalizesSavedNames(t *testing.T) {
	store := testStore(t)
	for _, body := range []string{`{"host":"127.0.0.1"}`, `{"server_name":""}`, `{"server_name":" \t "}`} {
		if _, err := store.db.Exec(`INSERT INTO settings VALUES('server',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, body); err != nil {
			t.Fatal(err)
		}
		if settings, err := store.Settings(); err != nil || settings.ServerName != "Home" {
			t.Fatal("legacy or empty server name lost its default", settings, err)
		}
	}
	for _, tc := range []struct{ name, want string }{{"  办公室 · Mac  ", "办公室 · Mac"}, {"", "Home"}, {" \t ", "Home"}} {
		settings := DefaultSettings()
		settings.ServerName = tc.name
		if err := store.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		if saved, err := store.Settings(); err != nil || saved.ServerName != tc.want {
			t.Fatal("server name did not persist", saved, err)
		}
	}
}

func TestServerNameChangesReachConnectedClientsWithoutRestart(t *testing.T) {
	service, settings := settingsService(t)
	app := service.app
	user := testUser(t, service.Store, "alice")
	cookie, csrf := loginRequest(t, app, user.Username)
	server := httptest.NewServer(app)
	defer server.Close()
	conn := realtimeConnect(t, server, cookie, csrf, true)
	assertName := func(want string) {
		t.Helper()
		message := realtimeUntil(t, conn, func(m realtimeMessage) bool { return m.Type == "server" })
		var info map[string]string
		if err := json.Unmarshal(message.Data, &info); err != nil || info["server_name"] != want || len(info) != 1 {
			t.Fatal("incorrect server metadata", string(message.Data), err)
		}
		for _, path := range []string{"/api/version", "/api/me"} {
			response := apiRequest(app, "GET", path, "", cookie, "")
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 || body["server_name"] != want {
				t.Fatal("incorrect server name", path, response.Body.String(), err)
			}
		}
	}
	assertName("Home")
	realtimeReady(t, conn)
	for _, tc := range []struct{ name, want string }{{"  办公室 · Mac  ", "办公室 · Mac"}, {"", "Home"}} {
		settings.ServerName = tc.name
		if err := service.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		assertName(tc.want)
		if status := service.Status(); !status.Running || status.RestartRequired || service.app != app {
			t.Fatal("renaming restarted the service or left pending changes", status)
		}
	}
}
