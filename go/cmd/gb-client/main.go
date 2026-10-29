// gb-client is an interactive terminal client for the GB WebSocket server.
//
// Usage:
//
//	gb-client -server localhost:2010 -race Zorgons -governor 0
//
// If -race or -password are omitted, the client will prompt for them.
// The -password flag accepts the password on the command line (visible in
// process lists); omit it to be prompted instead (input is hidden).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unsafe"

	"github.com/gorilla/websocket"
)

func main() {
	server := flag.String("server", "localhost:2010", "server host:port")
	race := flag.String("race", "", "race name")
	password := flag.String("password", "", "password (omit to be prompted)")
	governor := flag.Int("governor", 0, "governor number")
	tls := flag.Bool("tls", false, "use TLS (wss:// / https://)")
	flag.Parse()

	reader := bufio.NewReader(os.Stdin)

	if *race == "" {
		fmt.Print("Race: ")
		line, _ := reader.ReadString('\n')
		*race = strings.TrimSpace(line)
	}
	if *password == "" {
		*password = promptPassword("Password: ")
	}

	scheme, wsScheme := "http", "ws"
	if *tls {
		scheme, wsScheme = "https", "wss"
	}

	token := login(scheme+"://"+*server, *race, *password, *governor)

	wsURL := url.URL{
		Scheme:   wsScheme,
		Host:     *server,
		Path:     "/ws",
		RawQuery: "token=" + url.QueryEscape(token),
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	// Shut down cleanly on Ctrl+C.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		conn.Close()
		fmt.Println()
		os.Exit(0)
	}()

	// Print messages from the server.
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			text := string(msg)
			fmt.Print(text)
			if !strings.HasSuffix(text, "\n") {
				fmt.Println()
			}
		}
	}()

	// Read commands from stdin and send to the server.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
			break
		}
	}

	<-serverDone
}

// login performs the /auth/login handshake and returns the JWT token.
func login(baseURL, race, password string, governor int) string {
	body, _ := json.Marshal(map[string]interface{}{
		"race":     race,
		"password": password,
		"governor": governor,
	})

	resp, err := http.Post(baseURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("login failed: %s", resp.Status)
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Fatalf("login decode: %v", err)
	}
	if result.Token == "" {
		log.Fatal("login: empty token in response")
	}
	return result.Token
}

// promptPassword prints a prompt and reads a line with terminal echo disabled.
func promptPassword(prompt string) string {
	fmt.Print(prompt)

	// Save terminal state, disable echo, restore on return.
	var oldState syscall.Termios
	fd := uintptr(syscall.Stdin)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&oldState))); errno != 0 {
		// Terminal not available (e.g. piped input); read normally.
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		return strings.TrimSpace(line)
	}

	noEcho := oldState
	noEcho.Lflag &^= syscall.ECHO | syscall.ECHOE | syscall.ECHOK | syscall.ECHONL
	syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&noEcho)))
	defer func() {
		syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&oldState)))
		fmt.Println()
	}()

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}
