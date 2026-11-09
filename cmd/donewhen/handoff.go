package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runHandoff runs `donewhen handoff <sub-command>`. Only `import` exists: it
// reads a Markdown file and saves it through the REST API of a running server,
// so it needs DONEWHEN_URL and a token, not the database.
func runHandoff(args []string) {
	if len(args) == 0 || args[0] != "import" {
		fmt.Fprintln(os.Stderr, "usage: donewhen handoff import --initiative <name|id> --file <path> [--workspace <slug>] [--summary <text>]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("handoff import", flag.ExitOnError)
	ini := fs.String("initiative", "", "project (initiative) name or id")
	file := fs.String("file", "", "Markdown file to save as the next version")
	wsp := fs.String("workspace", os.Getenv("DONEWHEN_WORKSPACE"), "workspace slug (default: DONEWHEN_WORKSPACE)")
	summary := fs.String("summary", "Imported from HANDOFF.md", "one-line summary of this version")
	_ = fs.Parse(args[1:])
	if *ini == "" || *file == "" {
		log.Fatal("--initiative and --file are required")
	}
	base := strings.TrimRight(os.Getenv("DONEWHEN_URL"), "/")
	if base == "" {
		log.Fatal("set DONEWHEN_URL (the address of the DoneWhen server)")
	}
	token, err := handoffToken()
	if err != nil {
		log.Fatal(err)
	}
	path := *file
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}
	body, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	c := &handoffClient{http: &http.Client{Timeout: 30 * time.Second}, base: base, token: token, workspace: *wsp}
	version, name, err := c.importHandoff(*ini, string(body), *summary)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("saved handoff for %s as version %d (%d lines)\n", name, version, strings.Count(string(body), "\n")+1)
}

// handoffToken reads DONEWHEN_TOKEN, else ~/.config/donewhen/token.
func handoffToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv("DONEWHEN_TOKEN")); t != "" {
		return t, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".config", "donewhen", "token")); err == nil {
			if t := strings.TrimSpace(string(b)); t != "" {
				return t, nil
			}
		}
	}
	return "", errors.New("no token: set DONEWHEN_TOKEN or put one in ~/.config/donewhen/token")
}

type handoffClient struct {
	http      *http.Client
	base      string
	token     string
	workspace string
}

func (c *handoffClient) do(method, path string, in, out any) error {
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.workspace != "" {
		req.Header.Set("X-Workspace", c.workspace)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// importHandoff finds the initiative by id or name, then saves body as its
// next handoff version. It returns the version number and the initiative name.
func (c *handoffClient) importHandoff(initiative, body, summary string) (int, string, error) {
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do("GET", "/api/initiatives", nil, &list); err != nil {
		return 0, "", err
	}
	var id, name string
	for _, i := range list {
		if i.ID == initiative || strings.EqualFold(i.Name, initiative) {
			if id != "" {
				return 0, "", fmt.Errorf("%q matches more than one project; use the id", initiative)
			}
			id, name = i.ID, i.Name
		}
	}
	if id == "" {
		return 0, "", fmt.Errorf("no project %q in this workspace", initiative)
	}
	var saved struct {
		Version int `json:"version"`
	}
	if err := c.do("PUT", "/api/initiatives/"+url.PathEscape(id)+"/handoff",
		map[string]string{"body": body, "summary": summary}, &saved); err != nil {
		return 0, "", err
	}
	return saved.Version, name, nil
}
