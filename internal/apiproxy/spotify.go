package apiproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UpstreamResponse is a raw Spotify API response.
type UpstreamResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// SpotifyClient talks to the Spotify Web API using the client-credentials flow.
type SpotifyClient struct {
	ClientID     string
	ClientSecret string
	APIBase      string // e.g. https://api.spotify.com
	TokenURL     string // defaults to https://accounts.spotify.com/api/token
	HTTP         *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

func (s *SpotifyClient) http() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}

func (s *SpotifyClient) getToken(ctx context.Context, force bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.token != "" && time.Now().Before(s.exp) {
		return s.token, nil
	}
	tokenURL := s.TokenURL
	if tokenURL == "" {
		tokenURL = "https://accounts.spotify.com/api/token"
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.ClientID, s.ClientSecret)
	resp, err := s.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("spotify token endpoint: %s", resp.Status)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	s.token = tr.AccessToken
	s.exp = time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - time.Minute)
	return s.token, nil
}

// Get performs GET {APIBase}{path}?{q}, refreshing the token once on 401 and
// honoring Retry-After once on 429.
func (s *SpotifyClient) Get(ctx context.Context, path string, q url.Values) (UpstreamResponse, error) {
	token, err := s.getToken(ctx, false)
	if err != nil {
		return UpstreamResponse{}, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		u := s.APIBase + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return UpstreamResponse{}, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := s.http().Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt < 2 {
			if token, err = s.getToken(ctx, true); err != nil {
				return UpstreamResponse{}, err
			}
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			if ra < 1 {
				ra = 1
			}
			if ra > 10 {
				ra = 10
			}
			select {
			case <-ctx.Done():
				return UpstreamResponse{}, ctx.Err()
			case <-time.After(time.Duration(ra) * time.Second):
			}
			continue
		}
		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		return UpstreamResponse{Status: resp.StatusCode, ContentType: ct, Body: body}, nil
	}
	return UpstreamResponse{}, fmt.Errorf("spotify upstream failed: %w", lastErr)
}
