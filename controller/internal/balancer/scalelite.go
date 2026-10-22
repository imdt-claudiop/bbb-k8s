package balancer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Scalelite talks to the Servers API (Scalelite >= 1.5.1), authenticated by checksum like
// the BBB API: sha256(call + query + secret).
type Scalelite struct {
	URL    string // https://host/scalelite/api
	Secret string
	HTTP   *http.Client
}

func NewScalelite(url, secret string) *Scalelite {
	return &Scalelite{URL: strings.TrimRight(url, "/"), Secret: secret,
		HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (s *Scalelite) checksum(call, query string) string {
	sum := sha256.Sum256([]byte(call + query + s.Secret))
	return hex.EncodeToString(sum[:])
}

func (s *Scalelite) request(ctx context.Context, method, call, query string, body any) ([]byte, error) {
	url := fmt.Sprintf("%s/%s?%s", s.URL, call, query)
	if query == "" {
		url = fmt.Sprintf("%s/%s?checksum=%s", s.URL, call, s.checksum(call, ""))
	} else {
		url += "&checksum=" + s.checksum(call, query)
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("scalelite %s: %s: %s", call, resp.Status, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// server is the Servers API wire shape. load is a NUMBER when the poller has one and the
// STRING "unavailable" when it does not (measured live on v1.8.6), so it is typed loosely.
type server struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	State    string `json:"state"`
	Load     any    `json:"load"`
	Online   string `json:"online"`
	Meetings any    `json:"meetings"` // absent in some versions
}

// loadOf reads the loose load field: numbers pass through, anything else is zero.
func loadOf(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func (s *Scalelite) servers(ctx context.Context) ([]server, error) {
	out, err := s.request(ctx, http.MethodGet, "getServers", "", nil)
	if err != nil {
		// "No servers are configured" comes back 404 with an error body: an empty list.
		if strings.Contains(string(out), "No servers") {
			return nil, nil
		}
		return nil, err
	}
	var list []server
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("scalelite getServers: %w: %s", err, string(out))
	}
	return list, nil
}

func (s *Scalelite) find(ctx context.Context, apiURL string) (*server, error) {
	list, err := s.servers(ctx)
	if err != nil {
		return nil, err
	}
	norm := strings.TrimRight(apiURL, "/")
	for i := range list {
		if strings.TrimRight(list[i].URL, "/") == norm {
			return &list[i], nil
		}
	}
	return nil, nil
}

func (s *Scalelite) Lookup(ctx context.Context, apiURL string) (*Server, error) {
	sv, err := s.find(ctx, apiURL)
	if err != nil || sv == nil {
		return nil, err
	}
	load := loadOf(sv.Load)
	meetings := 0
	switch m := sv.Meetings.(type) {
	case float64:
		meetings = int(m)
	case string:
		meetings, _ = strconv.Atoi(m)
	}
	return &Server{ID: sv.ID, Host: sv.URL, State: sv.State, Load: load,
		Online: sv.Online == "online", Meetings: meetings}, nil
}

func (s *Scalelite) Register(ctx context.Context, apiURL, secret string) error {
	sv, err := s.find(ctx, apiURL)
	if err != nil {
		return err
	}
	if sv == nil {
		// Born disabled, as with rake; enable right after (phase 2 spec, "Hooks").
		if _, err := s.request(ctx, http.MethodPost, "addServer", "",
			map[string]any{"server": map[string]string{"url": apiURL, "secret": secret}}); err != nil {
			return err
		}
		if sv, err = s.find(ctx, apiURL); err != nil || sv == nil {
			return fmt.Errorf("scalelite addServer: server not listed after add: %w", err)
		}
	}
	if sv.State != "enabled" {
		_, err = s.request(ctx, http.MethodPost, "updateServer", "",
			map[string]any{"id": sv.ID, "server": map[string]string{"state": "enable"}})
	}
	return err
}

func (s *Scalelite) Suspend(ctx context.Context, apiURL string) error {
	sv, err := s.find(ctx, apiURL)
	if err != nil || sv == nil {
		return err // unknown: nothing to suspend
	}
	if sv.State == "cordoned" || sv.State == "disabled" {
		return nil
	}
	_, err = s.request(ctx, http.MethodPost, "updateServer", "",
		map[string]any{"id": sv.ID, "server": map[string]string{"state": "cordon"}})
	return err
}

func (s *Scalelite) Deregister(ctx context.Context, apiURL string) error {
	sv, err := s.find(ctx, apiURL)
	if err != nil || sv == nil {
		return err
	}
	_, err = s.request(ctx, http.MethodPost, "deleteServer", "", map[string]any{"id": sv.ID})
	return err
}
