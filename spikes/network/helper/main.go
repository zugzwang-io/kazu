// Command helper runs inside the trial network on Kazu's behalf. It reads one
// JSON request per line on stdin and writes one JSON response per line on
// stdout, so the host can drive it over a single stdio channel.
//
//	{"op":"counters","url":"http://10.0.0.200:9000/counters"}
//	{"op":"sql","dsn":"postgres://...","query":"select count(*) from checkout.orders"}
//	{"op":"http","method":"POST","url":"http://checkout:8080/orders","headers":{...},"body":"..."}
//	{"op":"dial","addr":"1.1.1.1:443","timeout_ms":2000}
//
// A dial that connects answers ok with result "connected"; one that fails
// answers with the error.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type request struct {
	Op        string            `json:"op"`
	URL       string            `json:"url,omitempty"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	DSN       string            `json:"dsn,omitempty"`
	Query     string            `json:"query,omitempty"`
	Addr      string            `json:"addr,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
}

type response struct {
	OK      bool   `json:"ok"`
	Status  int    `json:"status,omitempty"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
	Elapsed int64  `json:"elapsed_ms"`
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		start := time.Now()
		var req request
		var resp response
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			resp.Error = "bad request: " + err.Error()
		} else {
			resp = handle(req)
		}
		resp.Elapsed = time.Since(start).Milliseconds()
		_ = out.Encode(resp)
	}
}

func handle(req request) response {
	timeout := 5 * time.Second
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var (
		status int
		result string
		err    error
	)
	switch req.Op {
	case "counters":
		status, result, err = doHTTP(ctx, request{URL: req.URL})
	case "http":
		status, result, err = doHTTP(ctx, req)
	case "sql":
		result, err = doSQL(ctx, req.DSN, req.Query)
	case "dial":
		result, err = doDial(ctx, req.Addr)
	default:
		err = fmt.Errorf("unknown op %q", req.Op)
	}
	if err != nil {
		return response{Status: status, Error: err.Error()}
	}
	return response{OK: true, Status: status, Result: result}
}

func doHTTP(ctx context.Context, req request) (int, string, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	hr, err := http.NewRequestWithContext(ctx, method, req.URL, strings.NewReader(req.Body))
	if err != nil {
		return 0, "", err
	}
	for k, v := range req.Headers {
		hr.Header.Set(k, v)
	}
	r, err := http.DefaultClient.Do(hr)
	if err != nil {
		return 0, "", err
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	return r.StatusCode, strings.TrimSpace(string(b)), err
}

// doSQL returns the first column of the first row as text.
func doSQL(ctx context.Context, dsn, query string) (string, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer conn.Close(context.Background())
	var v any
	if err := conn.QueryRow(ctx, query).Scan(&v); err != nil {
		return "", err
	}
	return fmt.Sprint(v), nil
}

func doDial(ctx context.Context, addr string) (string, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	_ = c.Close()
	return "connected", nil
}
