package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

const controlPath = "/v1/control"
const maxControlTimeout = 5 * time.Minute

type controlConnection struct {
	Version int    `json:"version"`
	Address string `json:"address"`
	Session string `json:"session"`
	Token   string `json:"token"`
}

type controlRequest struct {
	Version       int    `json:"version"`
	Session       string `json:"session"`
	Action        string `json:"action"`
	Entry         string `json:"entry,omitempty"`
	TimeoutMillis int64  `json:"timeoutMillis"`
}

type controlSnapshot struct {
	Online    bool                    `json:"online"`
	Entries   []contract.Entry        `json:"entries"`
	Instances []contract.InstanceInfo `json:"instances"`
	Recovery  []cordis.RecoveryInfo   `json:"recovery"`
}

type controlResponse struct {
	Version int              `json:"version"`
	Session string           `json:"session"`
	Phase   string           `json:"phase"`
	Code    contract.Code    `json:"code,omitempty"`
	Error   string           `json:"error,omitempty"`
	Status  *controlSnapshot `json:"status,omitempty"`
}

func controlAction(action string) bool {
	switch action {
	case "status", "enable", "disable", "stop", "resume", "retry":
		return true
	default:
		return false
	}
}

func controlCommand(ctx context.Context, flags *flag.FlagSet, parse func() (bool, error), out io.Writer) error {
	path := flags.String("connection", "", "explicit private host connection file")
	timeout := flags.Duration("timeout", 30*time.Second, "request timeout (up to 5m)")
	if done, err := parse(); done {
		return err
	}
	if *path == "" || *timeout < time.Millisecond || *timeout > maxControlTimeout ||
		flags.NArg() < 1 || flags.NArg() > 2 || !controlAction(flags.Arg(0)) ||
		flags.Arg(0) != "status" && flags.NArg() != 2 {
		return &usageError{message: commandUsage["control"]}
	}
	connection, err := readControlConnection(*path)
	if err != nil {
		return err
	}
	response, err := requestControl(ctx, connection, flags.Arg(0), flags.Arg(1), *timeout)
	if err != nil {
		return err
	}
	if err := writeJSON(out, response); err != nil {
		return fmt.Errorf("control response received (%s), output failed: %w", response.Phase, err)
	}
	if response.Error != "" {
		return fmt.Errorf("control %s: %s (%s); query status for the affected subtree before further changes", response.Phase, response.Error, response.Code)
	}
	return nil
}

func readControlConnection(path string) (controlConnection, error) {
	var connection controlConnection
	info, err := os.Lstat(path)
	if err != nil {
		return connection, fmt.Errorf("read control connection: %w", err)
	}
	if !info.Mode().IsRegular() {
		return connection, errors.New("control connection must be a regular file")
	}
	file, err := openControlFile(path)
	if err != nil {
		return connection, fmt.Errorf("open control connection: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return connection, errors.New("control connection changed while opening")
	}
	if err := checkControlFile(file); err != nil {
		return connection, fmt.Errorf("unsafe control connection: %w", err)
	}
	data, err := readControlBody(file)
	if err != nil {
		return connection, err
	}
	if err := contract.Decode(data, &connection); err != nil {
		return connection, fmt.Errorf("invalid control connection: %w", err)
	}
	host, portText, err := net.SplitHostPort(connection.Address)
	port, portErr := strconv.Atoi(portText)
	token, tokenErr := base64.RawURLEncoding.DecodeString(connection.Token)
	session, sessionErr := base64.RawURLEncoding.DecodeString(connection.Session)
	if connection.Version != 1 || err != nil || host != "127.0.0.1" ||
		portErr != nil || port < 1 || port > 65535 || strconv.Itoa(port) != portText ||
		tokenErr != nil || len(token) != 32 || sessionErr != nil || len(session) != 16 {
		return controlConnection{}, errors.New("invalid control connection fields")
	}
	return connection, nil
}

func readControlBody(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, contract.MaxFrameBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > contract.MaxFrameBytes {
		return nil, contract.Fail(contract.Invalid, "control message exceeds size limit")
	}
	return data, nil
}

func requestControl(ctx context.Context, connection controlConnection, action, entry string, timeout time.Duration) (controlResponse, error) {
	request := controlRequest{Version: 1, Session: connection.Session, Action: action, Entry: entry, TimeoutMillis: timeout.Milliseconds()}
	data, err := json.Marshal(request)
	if err != nil {
		return controlResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+connection.Address+controlPath, bytes.NewReader(data))
	if err != nil {
		return controlResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+connection.Token)
	req.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var response controlResponse
	res, err := client.Do(req)
	if err == nil {
		data, err = readControlBody(res.Body)
		err = errors.Join(err, res.Body.Close())
		if err == nil {
			err = contract.Decode(data, &response)
		}
		if err == nil && (response.Version != 1 || response.Session != connection.Session ||
			!validControlPhase(response.Phase) ||
			response.Error == "" && (response.Phase != "completed" || response.Code != "" || response.Status == nil || !response.Status.Online)) {
			err = errors.New("invalid control response")
		}
	}
	if err != nil {
		if action != "status" {
			return response, fmt.Errorf("control result unknown; query status for the affected subtree, do not automatically repeat: %w", err)
		}
		return response, fmt.Errorf("control status unavailable: %w", err)
	}
	return response, nil
}

func validControlPhase(phase string) bool {
	return phase == "completed" || phase == "rejected" || phase == "committed_with_error" || phase == "unknown"
}
