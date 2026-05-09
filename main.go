//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	//"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	configFile = "config.json"
	stateFile  = ".flingway.lock"
	xrayBinary = "xray.exe"
)

type Config struct {
	Inbounds  []Inbound  `json:"inbounds"`
	Outbounds []Outbound `json:"outbounds"`
}

type Inbound struct {
	Tag      string         `json:"tag"`
	Port     int            `json:"port"`
	Protocol string         `json:"protocol"`
	Listen   string         `json:"listen"`
	Settings map[string]any `json:"settings"`
}

type Outbound struct {
	Tag            string         `json:"tag,omitempty"`
	Protocol       string         `json:"protocol"`
	Settings       map[string]any `json:"settings"`
	StreamSettings map[string]any `json:"streamSettings,omitempty"`
}

type State struct {
	PID int `json:"pid"`
}

func getExecDir() string {
	execPath, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(execPath)
}

func getPath(filename string) string {
	return filepath.Join(getExecDir(), filename)
}

func main() {
	if len(os.Args) < 2 {
		printStatus()
		return
	}

	command := strings.ToLower(os.Args[1])

	switch command {
	case "start":
		if len(os.Args) < 3 {
			printusage()
			os.Exit(1)
		}
		proto := os.Args[2]
		if proto != "http" && proto != "socks" {
			fmt.Println("Error: protocol must be 'http' or 'socks'")
			os.Exit(1)
		}
		handleStart(proto)
	case "stop":
		if len(os.Args) < 3 {
			printusage()
			os.Exit(1)
		}
		proto := os.Args[2]
		if proto != "http" && proto != "socks" {
			fmt.Println("Error: protocol must be 'http' or 'socks'")
			os.Exit(1)
		}
		handleStop(proto)
	case "config":
		if len(os.Args) < 3 {
			fmt.Println("Usage: flingway config <-n URL | -r>")
			os.Exit(1)
		}
		action := os.Args[2]
		if action == "-n" {
			if len(os.Args) < 4 {
				fmt.Println("Error: missing VLESS URL")
				os.Exit(1)
			}
			handleAddConfig(os.Args[3])
		} else if action == "-r" {
			handleRemoveConfig()
		} else {
			fmt.Println("Usage: flingway config <-n URL | -r>")
			os.Exit(1)
		}
	case "--help", "-help", "help":
		printusage()
		fmt.Println("\033[32m Powered by Xray-core (MPL-2.0) \033[0m")
		fmt.Println("Project licensed under Apache-2.0")
	case "repo":
		fmt.Println("Github Repo: https://github.com/MaksimPchelka/Flingway")
		fmt.Println("Codeberg Repo: https://codeberg.org/maksimpchelka/Flingway")
	default:
		fmt.Println("For more information about commands type 'Flingway --help' or 'Flingway help'")
		os.Exit(1)
	}
}

func printusage() {
	fmt.Println(" ")
	fmt.Println("Flingway start <http|socks>: enable proxy")
	fmt.Println("Flingway stop <http|socks>: disable proxy")
	fmt.Println("Flingway config -n <vless://...>: add config")
	fmt.Println("Flingway config -r: remove config")
	fmt.Println("Flingway: show menu")
	fmt.Println("Flingway repo: give flingway repo")
}

func printStatus() {
	config := loadOrCreateConfig()
	httpStatus := "disabled"
	socksStatus := "disabled"

	for _, in := range config.Inbounds {
		if in.Tag == "http" {
			httpStatus = "enabled"
		}
		if in.Tag == "socks" {
			socksStatus = "enabled"
		}
	}

	xrayStatus := "disabled"
	if _, err := os.Stat(getPath(stateFile)); err == nil {
		xrayStatus = "enabled"
	}

	vlessStatus := "none"
	for _, out := range config.Outbounds {
		if out.Protocol == "vless" {
			if settings, ok := out.Settings["vnext"].([]any); ok && len(settings) > 0 {
				if vnext, ok := settings[0].(map[string]any); ok {
					if users, ok := vnext["users"].([]any); ok && len(users) > 0 {
						if user, ok := users[0].(map[string]any); ok {
							if id, ok := user["id"].(string); ok {
								if len(id) > 16 {
									vlessStatus = "vless://" + id[:16]
								} else {
									vlessStatus = "vless://" + id
								}
							}
						}
					}
				}
			}
			if vlessStatus == "none" {
				vlessStatus = "vless://configured"
			}
			break
		}
	}

	banner := []string{
		`           _______                                 `,
		`          / __/ (_)___  ____ __      ______ ___  __`,
		`         / /_/ / / __ \/ __ '/ | /| / / __ '/ / / /`,
		`        / __/ / / / / / /_/ /| |/ |/ / /_/ / /_/ / `,
		`       /_/ /_/_/_/ /_/\__, / |__/|__/\__/_/\__, /  `,
		`                     /____/               /____/   `,
		`                                                   `,
		`                      version 0.1.0                `,
		`                                                   `,
	}

	for _, line := range banner {
		fmt.Println("\033[1;37m" + line + "\033[0m")
	}

	fmt.Printf("HTTP Proxy: %s, SOCKS5 Proxy: %s, Xray: %s\n",
		colorize(httpStatus), colorize(socksStatus), colorize(xrayStatus))
	fmt.Printf("VLESS Config: %s...\n", vlessStatus)
}

func handleAddConfig(rawURL string) {
	config := loadOrCreateConfig()

	for _, out := range config.Outbounds {
		if out.Protocol == "vless" {
			fmt.Println("Error: VLESS config already stored. Delete it first using 'flingway config -r'.")
			os.Exit(1)
		}
	}

	outbound, err := parseVlessURL(rawURL)
	if err != nil {
		fmt.Printf("Error parsing VLESS URL: %v\n", err)
		os.Exit(1)
	}

	config.Outbounds = []Outbound{
		outbound,
		{Protocol: "freedom", Tag: "direct", Settings: map[string]any{}},
	}

	saveConfig(config)
	fmt.Println("Successfully added VLESS config.")

	if len(config.Inbounds) > 0 {
		restartXray()
	}
}

func handleRemoveConfig() {
	config := loadOrCreateConfig()

	found := false
	for _, out := range config.Outbounds {
		if out.Protocol == "vless" {
			found = true
			break
		}
	}

	if !found {
		fmt.Println("Error: No VLESS config found to remove.")
		os.Exit(1)
	}

	config.Outbounds = []Outbound{
		{Protocol: "freedom", Settings: map[string]any{}},
	}

	saveConfig(config)
	fmt.Println("Successfully removed VLESS config.")

	if len(config.Inbounds) > 0 {
		restartXray()
	}
}

func parseVlessURL(rawURL string) (Outbound, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Outbound{}, err
	}
	if u.Scheme != "vless" {
		return Outbound{}, errors.New("not a vless URL")
	}

	uuid := u.User.Username()
	host := u.Hostname()
	portStr := u.Port()
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 443
	}

	q := u.Query()
	netType := q.Get("type")
	if netType == "" {
		netType = "tcp"
	}
	security := q.Get("security")
	if security == "" {
		security = "none"
	}
	flow := q.Get("flow")

	user := map[string]any{
		"id":         uuid,
		"encryption": "none",
	}
	if flow != "" {
		user["flow"] = flow
	}

	outbound := Outbound{
		Tag:      "proxy",
		Protocol: "vless",
		Settings: map[string]any{
			"vnext": []map[string]any{
				{
					"address": host,
					"port":    port,
					"users": []map[string]any{
						user,
					},
				},
			},
		},
		StreamSettings: map[string]any{
			"network":  netType,
			"security": security,
		},
	}

	streamSettings := outbound.StreamSettings

	if security == "tls" {
		tlsSettings := map[string]any{
			"serverName": host,
		}
		if sni := q.Get("sni"); sni != "" {
			tlsSettings["serverName"] = sni
		}
		if fp := q.Get("fp"); fp != "" {
			tlsSettings["fingerprint"] = fp
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tlsSettings["alpn"] = strings.Split(alpn, ",")
		}
		streamSettings["tlsSettings"] = tlsSettings
	} else if security == "reality" {
		realitySettings := map[string]any{
			"serverName": host,
		}
		if sni := q.Get("sni"); sni != "" {
			realitySettings["serverName"] = sni
		}
		if fp := q.Get("fp"); fp != "" {
			realitySettings["fingerprint"] = fp
		}
		if pbk := q.Get("pbk"); pbk != "" {
			realitySettings["publicKey"] = pbk
		}
		if sid := q.Get("sid"); sid != "" {
			realitySettings["shortId"] = sid
		}
		if spx := q.Get("spx"); spx != "" {
			realitySettings["spiderX"] = spx
		}
		streamSettings["realitySettings"] = realitySettings
	}

	if netType == "xhttp" {
		xhttpSettings := map[string]any{}
		if path := q.Get("path"); path != "" {
			xhttpSettings["path"] = path
		}
		if mode := q.Get("mode"); mode != "" {
			xhttpSettings["mode"] = mode
		}
		if hostParam := q.Get("host"); hostParam != "" {
			xhttpSettings["host"] = hostParam
		}
		streamSettings["xhttpSettings"] = xhttpSettings
	} else if netType == "ws" {
		wsSettings := map[string]any{}
		if path := q.Get("path"); path != "" {
			wsSettings["path"] = path
		}
		if hostParam := q.Get("host"); hostParam != "" {
			wsSettings["headers"] = map[string]any{
				"Host": hostParam,
			}
		}
		streamSettings["wsSettings"] = wsSettings
	} else if netType == "grpc" {
		grpcSettings := map[string]any{}
		if serviceName := q.Get("serviceName"); serviceName != "" {
			grpcSettings["serviceName"] = serviceName
		}
		streamSettings["grpcSettings"] = grpcSettings
	} else if netType == "tcp" {
		if headerType := q.Get("headerType"); headerType != "" && headerType != "none" {
			tcpSettings := map[string]any{
				"header": map[string]any{
					"type": headerType,
				},
			}
			if hostParam := q.Get("host"); hostParam != "" {
				tcpSettings["header"].(map[string]any)["request"] = map[string]any{
					"headers": map[string]any{
						"Host": []string{hostParam},
					},
				}
			}
			streamSettings["tcpSettings"] = tcpSettings
		}
	}

	return outbound, nil
}

func handleStart(proto string) {
	config := loadOrCreateConfig()

	for _, in := range config.Inbounds {
		if in.Tag == proto {
			fmt.Printf("successfully started %s proxy on port %d\n", proto, in.Port)
			return
		}
	}

	port, err := findFreePort()
	if err != nil {
		fmt.Printf("Error finding free port: %v\n", err)
		os.Exit(1)
	}

	newInbound := Inbound{
		Tag:      proto,
		Port:     port,
		Protocol: proto,
		Listen:   "127.0.0.1",
		Settings: map[string]any{},
	}
	config.Inbounds = append(config.Inbounds, newInbound)

	saveConfig(config)
	restartXray()
	fmt.Printf("successfully started %s proxy on port %d\n", proto, port)
}

func handleStop(proto string) {
	config := loadOrCreateConfig()

	found := false
	var newInbounds []Inbound
	for _, in := range config.Inbounds {
		if in.Tag == proto {
			found = true
		} else {
			newInbounds = append(newInbounds, in)
		}
	}

	if !found {
		fmt.Printf("Error: %s proxy is not running\n", proto)
		os.Exit(1)
	}

	config.Inbounds = newInbounds
	saveConfig(config)

	if len(config.Inbounds) == 0 {
		stopXray()
		os.Remove(getPath(stateFile))
		fmt.Printf("successfully stopped %s proxy. Xray stopped.\n", proto)
	} else {
		restartXray()
		fmt.Printf("successfully stopped %s proxy.\n", proto)
	}
}

func loadOrCreateConfig() Config {
	data, err := os.ReadFile(getPath(configFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{
				Inbounds: []Inbound{},
				Outbounds: []Outbound{
					{
						Protocol: "freedom",
						Settings: map[string]any{},
					},
				},
			}
		}
		fmt.Printf("Error reading config: %v\n", err)
		os.Exit(1)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		fmt.Printf("Error parsing config: %v\n", err)
		os.Exit(1)
	}

	if len(config.Outbounds) == 0 {
		config.Outbounds = []Outbound{
			{
				Protocol: "freedom",
				Settings: map[string]any{},
			},
		}
	}

	return config
}

func saveConfig(config Config) {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		fmt.Printf("Error serializing config: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(getPath(configFile), data, 0644); err != nil {
		fmt.Printf("Error writing config: %v\n", err)
		os.Exit(1)
	}
}

func findFreePort() (int, error) {
	for port := 10000; port <= 20000; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			ln.Close()
			return port, nil
		}
	}
	return 0, errors.New("no free ports available in range 10000-20000")
}

func stopXray() {
	stateData, err := os.ReadFile(getPath(stateFile))
	if err != nil {
		return
	}

	var state State
	if err := json.Unmarshal(stateData, &state); err != nil {
		return
	}

	cmd := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(state.PID), "/FI", fmt.Sprintf("IMAGENAME eq %s", xrayBinary))
	cmd.Run()
}

func restartXray() {
	stopXray()

	xrayPath := getPath(xrayBinary)
	if _, err := os.Stat(xrayPath); os.IsNotExist(err) {
		xrayPath = xrayBinary
	}

	cmd := exec.Command(xrayPath, "-c", getPath(configFile))

	logFile, err := os.OpenFile(getPath("xray.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting Xray process: %v\n", err)
		os.Exit(1)
	}

	state := State{PID: cmd.Process.Pid}
	if stateData, err := json.Marshal(state); err == nil {
		os.WriteFile(getPath(stateFile), stateData, 0644)
	}

	time.Sleep(1 * time.Second)

	cmd.Process.Release()
}

func colorize(status string) string {
	reset := "\033[0m"
	red := "\033[31m"
	green := "\033[32m"
	yellow := "\033[33m"

	if status == "enabled" {
		return green + status + reset
	}
	if status == "disabled" {
		return red + status + reset
	}
	if status == "could not be verified" {
		return yellow + status + reset
	}

	return status
}
