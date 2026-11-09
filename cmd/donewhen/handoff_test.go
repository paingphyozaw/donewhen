package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImportHandoff(t *testing.T) {
	var gotBody map[string]string
	var gotAuth, gotWS, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotWS = r.Header.Get("Authorization"), r.Header.Get("X-Workspace")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/initiatives":
			_, _ = io.WriteString(w, `[{"id":"i-1","name":"Finance"},{"id":"i-2","name":"POS"}]`)
		case r.Method == "PUT":
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_, _ = io.WriteString(w, `{"version":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &handoffClient{http: srv.Client(), base: srv.URL, token: "tok", workspace: "biz"}
	v, name, err := c.importHandoff("pos", "# H\n\ntext", "Imported from HANDOFF.md")
	if err != nil || v != 1 || name != "POS" {
		t.Fatalf("import = %d %q %v", v, name, err)
	}
	if gotPath != "/api/initiatives/i-2/handoff" || gotBody["body"] != "# H\n\ntext" ||
		gotBody["summary"] != "Imported from HANDOFF.md" || gotAuth != "Bearer tok" || gotWS != "biz" {
		t.Fatalf("request = %s %v %s %s", gotPath, gotBody, gotAuth, gotWS)
	}
	if _, _, err := c.importHandoff("Nope", "x", "y"); err == nil || !strings.Contains(err.Error(), "no project") {
		t.Fatalf("unknown project err = %v", err)
	}
}
