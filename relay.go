package main

import (
	"errors"
	"go.uber.org/zap"
	"io"
	"sync"

	"github.com/ReneKroon/ttlcache"
	"github.com/olahol/melody"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"log"
	"time"
)

// WSRelay represents our WebSocket relay service.
type WSRelay struct {
	// The cache for active relay connections.
	cache *ttlcache.Cache
	// Melody instance to manage WebSocket connections.
	Melody *melody.Melody
	// Mutex for synchronizing access to the cache
	mu                   sync.RWMutex
	logger               *zap.SugaredLogger
	tokenCounter         prometheus.Counter
	tokenRemovedCounter  prometheus.Counter
	messageSizeHistogram prometheus.Histogram
	tokenBytesCounter    *prometheus.CounterVec
	tokenMessagesCounter *prometheus.CounterVec
}

// NewWSRelay creates a new instance of WSRelay.
func NewWSRelay(logger *zap.SugaredLogger) *WSRelay {
	tokenRemovedCounter := promauto.NewCounter(prometheus.CounterOpts{
		Name: "wsrelay_token_disposed_total",
		Help: "The total number of tokens disposed of",
	})
	cache := ttlcache.NewCache()
	cache.SetTTL(15 * time.Minute)
	mu := sync.RWMutex{}
	cache.SetCheckExpirationCallback(func(key string, value interface{}) bool {
		mu.RLock()
		defer mu.RUnlock()
		conn, ok := value.(*wsRelayConn)
		if !ok {
			log.Println("Failed to convert cached value to connection for token:", key)
			tokenRemovedCounter.Inc()
			return false
		}
		if conn.session == nil || conn.session.IsClosed() {
			log.Println("Session is not active for token:", key)
			tokenRemovedCounter.Inc()
			return false // let it expire
		}
		return true // prevent from expiring
	})

	return &WSRelay{
		cache:  cache,
		Melody: melody.New(),
		logger: logger.Named("ws-relay"),
		tokenCounter: promauto.NewCounter(prometheus.CounterOpts{
			Name: "wsrelay_token_total",
			Help: "The total number of tokens created",
		}),
		tokenRemovedCounter: tokenRemovedCounter,
		messageSizeHistogram: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "wsrelay_message_size_bytes",
			Help:    "The size of the messages received",
			Buckets: prometheus.ExponentialBuckets(64, 2, 10), // 64 bytes to ~64KB
		}),
		tokenBytesCounter: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "wsrelay_token_bytes_total",
			Help: "The total number of bytes per token",
		}, []string{"token"}),
		tokenMessagesCounter: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "wsrelay_token_messages_total",
			Help: "The total number of messages per token",
		}, []string{"token"}),
	}
}

// wsRelayConn represents a single WebSocket relay connection.
type wsRelayConn struct {
	// The WebSocket connection.
	session *melody.Session
}

// newWSRelayConn creates a new instance of wsRelayConn.
func newWSRelayConn(session *melody.Session) *wsRelayConn {
	return &wsRelayConn{
		session: session,
	}
}

// RegisterToken registers a token with an associated connection.
func (r *WSRelay) RegisterToken(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache.Set(token, newWSRelayConn(nil))
	r.logger.Infow("Registered token", "token", token)
	r.tokenCounter.Inc()
}

// RegisterSession associates a session with an existing token.
func (r *WSRelay) RegisterSession(token string, session *melody.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.cache.Get(token)
	if !exists {
		r.logger.Warnw("Token not found", "token", token)
		return errors.New("token not found")
	}

	conn, ok := value.(*wsRelayConn)
	if !ok {
		r.logger.Warnw("Failed to convert cached value to connection", "token", token)
		return errors.New("failed to convert cached value to connection")
	}

	conn.session = session
	r.logger.Infow("Registered session for token", "token", token)
	return nil
}

func (r *WSRelay) SendData(token string, body io.Reader) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	value, exists := r.cache.Get(token)
	if !exists {
		r.logger.Warnw("Token not found", "token", token)
		return errors.New("token not found")
	}

	conn, ok := value.(*wsRelayConn)
	if !ok || conn.session == nil {
		r.logger.Warnw("No session associated with this token", "token", token)
		return errors.New("no session associated with this token")
	}

	buf := make([]byte, 4*1024)

	r.logger.Debugw("Starting send", "token", token)
	totalBytes := 0
	count := 0
	for {
		count++
		n, err := body.Read(buf)
		if n > 0 {
			r.logger.Debugw("writing bytes", "token", token, "byte_count", n)
			totalBytes += n
			err = conn.session.Write(buf[:n])
			if err != nil {
				r.logger.Errorw("Error writing data to session", "token", token, "error", err)
				return err
			}
			r.messageSizeHistogram.Observe(float64(n))
			r.tokenBytesCounter.WithLabelValues(token).Add(float64(n))
			r.tokenMessagesCounter.WithLabelValues(token).Inc()
		} else {
			r.logger.Debugw("got back empty bytes", "token", token)

		}
		if err != nil {
			if err == io.EOF {
				r.logger.Debugw("Got EOF", "token", token)
				break
			} else {
				r.logger.Errorw("Error while reading from body, ending read", "error", err)
				return err
			}
		}
	}
	r.logger.Debugw("Finished send", "token", token)

	return nil
}

// DisposeToken removes a token and its associated session.
func (r *WSRelay) DisposeToken(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache.Remove(token)
	r.tokenRemovedCounter.Inc()
	r.logger.Infow("Disposed token:", "token", token)
}
