package net

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const tokenTTL = 7 * 24 * time.Hour // tokens valid for 7 days

// Claims are the JWT payload fields embedded in every token.
type Claims struct {
	jwt.RegisteredClaims
	PlayerNum int  `json:"player_num"`
	Governor  int  `json:"governor"`
	God       bool `json:"god"`
}

// issueToken creates a signed JWT for the given player/governor.
func (srv *Server) issueToken(playerNum, governor int, god bool) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
		},
		PlayerNum: playerNum,
		Governor:  governor,
		God:       god,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(srv.cfg.JWTSecret))
}

// parseToken validates a JWT string and returns its claims.
func (srv *Server) parseToken(tokenStr string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(srv.cfg.JWTSecret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// tokenFromRequest extracts the Bearer token from the Authorization header,
// falling back to the "token" query parameter (useful for WS upgrade URLs).
func tokenFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// handleLogin handles POST /auth/login.
// Body: {"race": "Zorgons", "password": "secret", "governor": 0}
// Response: {"token": "eyJ..."}
func (srv *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Race     string `json:"race"`
		Password string `json:"password"`
		Governor int    `json:"governor"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Delegate authentication to the game state actor.
	authResp := srv.game.Authenticate(req.Race, req.Password, req.Governor)
	if !authResp.OK {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := srv.issueToken(authResp.PlayerNum, req.Governor, authResp.God)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"token": token})
}
