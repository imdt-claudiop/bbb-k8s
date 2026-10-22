package balancer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeScalelite is a minimal Servers API: checksum-checked, state machine add/update/delete.
type fakeScalelite struct {
	secret  string
	servers map[string]*server
	nextID  int
}

func (f *fakeScalelite) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := strings.TrimPrefix(r.URL.Path, "/scalelite/api/")
		q := r.URL.Query()
		cs := q.Get("checksum")
		q.Del("checksum")
		sum := sha256.Sum256([]byte(call + q.Encode() + f.secret))
		if cs != hex.EncodeToString(sum[:]) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch call {
		case "getServers":
			if len(f.servers) == 0 {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"error":"No servers are configured"}`)
				return
			}
			var list []*server
			for _, s := range f.servers {
				list = append(list, s)
			}
			_ = json.NewEncoder(w).Encode(list)
		case "addServer":
			var body struct {
				Server struct{ URL, Secret string } `json:"server"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.nextID++
			id := fmt.Sprintf("id-%d", f.nextID)
			// Load as a NUMBER, as the live API answers when the poller has one (the string
			// "unavailable" is the other live shape, covered by the empty default).
			f.servers[id] = &server{ID: id, URL: body.Server.URL, State: "disabled", Load: 0.0, Online: "online"}
			_ = json.NewEncoder(w).Encode(f.servers[id])
		case "updateServer":
			var body struct {
				ID     string                 `json:"id"`
				Server struct{ State string } `json:"server"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sv, ok := f.servers[body.ID]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			switch body.Server.State {
			case "enable":
				sv.State = "enabled"
			case "cordon":
				sv.State = "cordoned"
			}
			_ = json.NewEncoder(w).Encode(sv)
		case "deleteServer":
			var body struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := f.servers[body.ID]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(f.servers, body.ID)
			fmt.Fprint(w, `{"success":true}`)
		default:
			t.Errorf("unexpected call %q", call)
			w.WriteHeader(http.StatusTeapot)
		}
	})
}

func TestScaleliteLifecycle(t *testing.T) {
	fake := &fakeScalelite{secret: "s3cr3t", servers: map[string]*server{}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	sc := NewScalelite(srv.URL+"/scalelite/api", "s3cr3t")
	ctx := context.Background()
	api := "https://cell-9.example/bigbluebutton/api"

	// Empty list: lookup nil, suspend/deregister no-ops.
	if sv, err := sc.Lookup(ctx, api); err != nil || sv != nil {
		t.Fatalf("lookup on empty: %v %v", sv, err)
	}
	if err := sc.Suspend(ctx, api); err != nil {
		t.Fatalf("suspend on empty: %v", err)
	}
	if err := sc.Deregister(ctx, api); err != nil {
		t.Fatalf("deregister on empty: %v", err)
	}

	// Register: born disabled, enabled right after; idempotent.
	for range 2 {
		if err := sc.Register(ctx, api, "cellsecret"); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	if len(fake.servers) != 1 {
		t.Fatalf("register not idempotent: %d servers", len(fake.servers))
	}
	sv, err := sc.Lookup(ctx, api)
	if err != nil || sv == nil || sv.State != "enabled" || sv.Load != 0 {
		t.Fatalf("after register: %+v %v", sv, err)
	}

	// Suspend: cordoned; idempotent.
	for range 2 {
		if err := sc.Suspend(ctx, api); err != nil {
			t.Fatalf("suspend: %v", err)
		}
	}
	if sv, _ := sc.Lookup(ctx, api); sv.State != "cordoned" {
		t.Fatalf("after suspend: %+v", sv)
	}

	// Deregister: gone; idempotent.
	for range 2 {
		if err := sc.Deregister(ctx, api); err != nil {
			t.Fatalf("deregister: %v", err)
		}
	}
	if sv, _ := sc.Lookup(ctx, api); sv != nil {
		t.Fatalf("still listed after deregister: %+v", sv)
	}
}

func TestScaleliteBadChecksumRejected(t *testing.T) {
	fake := &fakeScalelite{secret: "right", servers: map[string]*server{}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	sc := NewScalelite(srv.URL+"/scalelite/api", "wrong")
	if err := sc.Register(context.Background(), "https://x/bigbluebutton/api", "s"); err == nil {
		t.Fatal("expected checksum rejection")
	}
}
