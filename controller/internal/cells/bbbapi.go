package cells

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BBBClient reads a cell through its own BigBlueButton API: meetings and participants come
// from the cell, never from the balancer, so retirement stays balancer-agnostic.
type BBBClient struct {
	Host   string // public host
	Secret string
	HTTP   *http.Client
}

func NewBBBClient(host, secret string, insecure bool) *BBBClient {
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &BBBClient{Host: host, Secret: secret, HTTP: &http.Client{Timeout: 30 * time.Second, Transport: tr}}
}

// APIURL is the base URL the balancer registers.
func (b *BBBClient) APIURL() string { return fmt.Sprintf("https://%s/bigbluebutton/api", b.Host) }

type meetingsResponse struct {
	ReturnCode string `xml:"returncode"`
	Meetings   []struct {
		ParticipantCount int `xml:"participantCount"`
	} `xml:"meetings>meeting"`
}

// Meetings returns the number of running meetings and the participants across them.
func (b *BBBClient) Meetings(ctx context.Context) (meetings, participants int, err error) {
	call, query := "getMeetings", ""
	sum := sha1.Sum([]byte(call + query + b.Secret))
	url := fmt.Sprintf("%s/%s?checksum=%s", b.APIURL(), call, hex.EncodeToString(sum[:]))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, 0, err
	}
	var out meetingsResponse
	if err := xml.Unmarshal(raw, &out); err != nil {
		return 0, 0, fmt.Errorf("getMeetings on %s: %w", b.Host, err)
	}
	if out.ReturnCode != "SUCCESS" {
		return 0, 0, fmt.Errorf("getMeetings on %s: %s", b.Host, out.ReturnCode)
	}
	for _, m := range out.Meetings {
		participants += m.ParticipantCount
	}
	return len(out.Meetings), participants, nil
}
