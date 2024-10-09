package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"time"

	"golang.org/x/sync/errgroup"
)

type firebaseProxy struct {
	projects map[string]int // projectID to port
	port     int            // proxy port
}

func (p *firebaseProxy) addProject(projectID string, port int) {
	p.projects[projectID] = port
}

func waitUntilReady(ctx context.Context, url string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			resp, err := http.Get(url)
			if err == nil {
				io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					return nil
				}
			}
			time.Sleep(time.Millisecond * 500)
		}
	}
}

func (p *firebaseProxy) ready(ctx context.Context) <-chan error {
	ch := make(chan error)
	go func() {
		ch <- waitUntilReady(ctx, fmt.Sprintf("http://localhost:%d/health_check", p.port))
	}()
	return ch
}

func (p *firebaseProxy) listenAndServe() error {
	addr := fmt.Sprintf(":%d", p.port)
	host := fmt.Sprintf("localhost:%d", p.port)

	// 各プロジェクトのエミュレーターが起動したことを確認
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	eg, ctx := errgroup.WithContext(ctx)
	for projectID, port := range p.projects {
		url := fmt.Sprintf("http://localhost:%d/emulator/v1/projects/%s/config", port, projectID)

		eg.Go(func() error {
			return waitUntilReady(ctx, url)
		})
	}
	err := eg.Wait()
	if err != nil {
		return err
	}

	// Firebase Admin SDKに、プロキシのURLをエミュレーターのものとして認識させる。
	os.Setenv("FIREBASE_AUTH_EMULATOR_HOST", host)

	// プロキシを起動
	projectsPathRx := regexp.MustCompile("^/identitytoolkit.googleapis.com/v1/projects/([^/]+)")
	accountsPathRx := regexp.MustCompile("^/identitytoolkit.googleapis.com/v1/accounts")

	return http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health_check" {
			return
		}

		var targetProjectID string
		if matches := projectsPathRx.FindStringSubmatch(r.URL.Path); len(matches) > 0 {
			targetProjectID = matches[1]
		} else if accountsPathRx.MatchString(r.URL.Path) {
			targetProjectID = r.URL.Query().Get("key") // apiKeyがプロジェクトIDとなる
		}

		port, ok := p.projects[targetProjectID]
		if !ok {
			dump, _ := httputil.DumpRequest(r, true)
			panic(fmt.Sprintf("firebaseProxy: unknown request received:\n%s", dump))
		}

		req := r.Clone(r.Context())
		req.RequestURI = ""
		req.URL, _ = url.Parse(fmt.Sprintf("http://localhost:%d%s?%s", port, r.URL.Path, r.URL.Query().Encode()))

		resp, _ := http.DefaultClient.Do(req)

		conn, _, _ := w.(http.Hijacker).Hijack()
		resp.Write(conn)
		conn.Close()
	}))
}

type signInResponse struct {
	IDToken string `json:"idToken"`
}

func (p *firebaseProxy) issueIDToken(ctx context.Context, projectID, email, password string) (string, error) {
	reqBody, err := json.Marshal(map[string]any{
		"email":             email,
		"password":          password,
		"returnSecureToken": true,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost,
		fmt.Sprintf("http://localhost:%d/identitytoolkit.googleapis.com/v1/accounts:signInWithPassword?key=%s", p.port, projectID),
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var r signInResponse
	err = json.NewDecoder(resp.Body).Decode(&r)
	if err != nil {
		return "", err
	}
	return r.IDToken, nil
}
