package net

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/wirejson"
)

const tokenTTL = 7 * 24 * time.Hour // tokens valid for 7 days

// Claims are the JWT payload fields embedded in every token.
type Claims struct {
	jwt.RegisteredClaims
	PlayerNum int    `json:"player_num"`
	Governor  int    `json:"governor"`
	God       bool   `json:"god"`
	Revision  string `json:"revision"`
}

// issueToken creates a signed JWT for the given player/governor.
func (srv *Server) issueToken(playerNum, governor int, god bool, revision string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
		},
		PlayerNum: playerNum,
		Governor:  governor,
		God:       god,
		Revision:  revision,
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
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid || claims.ExpiresAt == nil || !enrollmentapi.IsHexDigest(claims.Revision) || claims.PlayerNum < 1 || claims.PlayerNum > model.MaxPlayers || claims.Governor < 0 || claims.Governor > model.MaxGovernors {
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
		Race             string  `json:"race"`
		Governor         int     `json:"governor"`
		Password         *string `json:"password,omitempty"`
		RacePassword     *string `json:"race_password,omitempty"`
		GovernorPassword *string `json:"governor_password,omitempty"`
	}
	data, readErr := io.ReadAll(io.LimitReader(r.Body, 4097))
	if readErr != nil || len(data) > 4096 || wirejson.Decode(data, &req) != nil || req.Race == "" || req.Governor < 0 || req.Governor > model.MaxGovernors {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	legacy := req.Password != nil && req.RacePassword == nil && req.GovernorPassword == nil
	dual := req.Password == nil && req.RacePassword != nil && req.GovernorPassword != nil
	if !legacy && !dual {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	valid := func(s *string) bool {
		return s != nil && len(*s) > 0 && len(*s) <= 72 && !strings.ContainsAny(*s, "\x00\r\n")
	}
	if legacy && !valid(req.Password) || dual && (!valid(req.RacePassword) || !valid(req.GovernorPassword)) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var authResp AuthResult
	var err error
	if dual {
		authResp, err = srv.game.AuthenticateDual(ctx, req.Race, *req.RacePassword, *req.GovernorPassword, req.Governor)
	} else {
		authResp, err = srv.game.Authenticate(ctx, req.Race, *req.Password, req.Governor)
	}
	if err != nil {
		srv.metrics.Logins.Inc("unavailable")
		http.Error(w, "server busy, retry shortly", http.StatusServiceUnavailable)
		return
	}
	if !authResp.OK {
		srv.metrics.Logins.Inc("denied")
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := srv.issueToken(authResp.PlayerNum, req.Governor, authResp.God, authResp.Revision)
	if err != nil {
		srv.metrics.Logins.Inc("error")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	srv.metrics.Logins.Inc("ok")

	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}
