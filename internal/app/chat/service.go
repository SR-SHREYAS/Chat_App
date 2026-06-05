package chat

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"real_time_chat_app/internal/model"
)

const (
	SocketBufferSize      = 1024
	MessageBufferSize     = 256
	DefaultSignedRoomTTL  = 10 * time.Minute
	MaxSignedRoomTTL      = 7 * 24 * time.Hour
	MaxSignedRoomCapacity = 10 * 24 * time.Hour
	SignedRoomCodeLength  = model.SignedRoomEntryCodeLength
	signedRoomCleanupTTL  = 1 * time.Minute
	roomHistoryLimit      = 15
	roomHistoryRoleOwned  = "owned"
	roomHistoryRoleJoined = "joined"
)

var (
	ErrSignedRoomUnavailable      = errors.New("signed room service unavailable")
	ErrInvalidRoomName            = errors.New("invalid room name")
	ErrInvalidRoomOwner           = errors.New("invalid room owner")
	ErrSignedRoomTTLTooLarge      = errors.New("signed room ttl exceeds maximum")
	ErrSignedRoomNotFound         = errors.New("signed room not found")
	ErrSignedRoomExpired          = errors.New("signed room expired")
	ErrSignedRoomAlreadyActive    = errors.New("signed room already active")
	ErrSignedRoomCapacityTooLarge = errors.New("signed room active capacity exceeds maximum")
	ErrRoomOwnedByAnotherUser     = errors.New("room is owned by another user")
	ErrInvalidRoomEntryCode       = errors.New("invalid room entry code")
)

type MessageStore interface {
	SaveMessage(ctx context.Context, roomName, userName, message string) error
	GetRecentMessages(ctx context.Context, roomName string, limit int) ([]model.Message, error)
	Ping(ctx context.Context) error
}

type SignedRoomStore interface {
	CreateSignedRoom(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName, entryCode string, expiresAt time.Time) (model.SignedRoom, error)
	GetSignedRoomByName(ctx context.Context, roomName string) (model.SignedRoom, error)
	UpdateSignedRoomExpiry(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName, entryCode string, expiresAt time.Time) (model.SignedRoom, error)
	ListOwnedSignedRooms(ctx context.Context, ownerUserID int64) ([]model.SignedRoom, error)
	DeleteSignedRoomByName(ctx context.Context, roomName string) error
	DeleteExpiredSignedRooms(ctx context.Context, now time.Time) (int64, error)
	RecordRoomMembership(ctx context.Context, userID int64, roomName, role string) error
	GetRoomMembership(ctx context.Context, userID int64, roomName, role string) (model.RoomHistory, error)
	PruneRoomMemberships(ctx context.Context, userID int64, limit int) error
	ListRoomMemberships(ctx context.Context, userID int64, limit int) ([]model.RoomHistory, error)
}

type Service struct {
	rooms     *Registry
	store     MessageStore
	roomStore SignedRoomStore

	cleanupMu              sync.Mutex
	lastSignedRoomCleanup  time.Time
	signedRoomCleanupEvery time.Duration
}

func NewService(store MessageStore) *Service {
	return &Service{
		rooms:                  NewRegistry(),
		store:                  store,
		signedRoomCleanupEvery: signedRoomCleanupTTL,
	}
}

func (s *Service) BindSignedRoomStore(store SignedRoomStore) {
	s.roomStore = store
}

func (s *Service) newClient(socket *websocket.Conn, room *Room, userID, userName string) *Client {
	name := strings.TrimSpace(userName)
	if name == "" {
		name = fmt.Sprintf("user-%s", userID)
	}

	return &Client{
		socket:   socket,
		room:     room,
		receive:  make(chan []byte, MessageBufferSize),
		name:     name,
		messages: s.store,
	}
}

func (s *Service) sendRecentMessages(ctx context.Context, c *Client) {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	messages, err := s.store.GetRecentMessages(queryCtx, c.room.name, 50)
	if err != nil {
		log.Printf("Could not query recent messages for room %s: %v", c.room.name, err)
		return
	}

	count := 0
	for _, m := range messages {
		msgJSON, err := json.Marshal(map[string]string{
			"name":    m.UserName,
			"message": m.Message,
		})
		if err != nil {
			log.Printf("Error marshaling message for user %s: %v", m.UserName, err)
			continue
		}
		c.receive <- msgJSON
		count++
	}

	log.Printf("Sent %d recent messages to %s in room %s", count, c.name, c.room.name)
}

func (s *Service) HandleRoom(ctx context.Context, socket *websocket.Conn, roomName, userID, userName string) (*Room, *Client) {
	room := s.rooms.GetOrCreate(roomName)
	client := s.newClient(socket, room, userID, userName)
	s.sendRecentMessages(ctx, client)
	return room, client
}

func (s *Service) HandleHealth(ctx context.Context) error {
	return s.store.Ping(ctx)
}

func (s *Service) HandleCreateSignedRoom(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName string, ttl time.Duration) (model.SignedRoom, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, ErrInvalidRoomName
	}
	if ownerUserID <= 0 {
		return model.SignedRoom{}, ErrInvalidRoomOwner
	}

	normalizedTTL, err := normalizeSignedRoomTTL(ttl)
	if err != nil {
		return model.SignedRoom{}, err
	}

	now := time.Now().UTC()
	if err := s.maybeCleanupExpiredSignedRooms(ctx, now); err != nil {
		log.Printf("Best-effort signed room cleanup failed before create for room %s: %v", roomName, err)
	}

	expiresAt := now.Add(normalizedTTL)
	entryCode, err := generateRoomEntryCode()
	if err != nil {
		return model.SignedRoom{}, err
	}

	existing, err := s.roomStore.GetSignedRoomByName(ctx, roomName)
	var room model.SignedRoom
	if err == nil {
		if existing.OwnerUserID != ownerUserID {
			return model.SignedRoom{}, ErrRoomOwnedByAnotherUser
		}
		room, err = s.roomStore.UpdateSignedRoomExpiry(ctx, roomName, ownerUserID, ownerDisplayName, entryCode, expiresAt)
	} else if errors.Is(err, sql.ErrNoRows) {
		room, err = s.roomStore.CreateSignedRoom(ctx, roomName, ownerUserID, ownerDisplayName, entryCode, expiresAt)
	} else {
		return model.SignedRoom{}, err
	}

	if err != nil {
		return model.SignedRoom{}, err
	}

	s.recordRoomMembershipBestEffort(ctx, ownerUserID, roomName, roomHistoryRoleOwned)
	s.recordRoomMembershipBestEffort(ctx, ownerUserID, roomName, roomHistoryRoleJoined)

	return room, nil
}

func (s *Service) HandleJoinSignedRoom(ctx context.Context, roomName, entryCode string) (model.SignedRoom, error) {
	room, found, err := s.HandleGetSignedRoomStatus(ctx, roomName)
	if err != nil {
		return model.SignedRoom{}, err
	}
	if !found {
		return model.SignedRoom{}, ErrSignedRoomNotFound
	}

	normalizedCode, err := normalizeRoomEntryCode(entryCode)
	if err != nil {
		return model.SignedRoom{}, err
	}
	if room.EntryCode != normalizedCode {
		return model.SignedRoom{}, ErrInvalidRoomEntryCode
	}

	return room, nil
}

func (s *Service) HandleListOwnedSignedRooms(ctx context.Context, ownerUserID int64) ([]model.SignedRoom, error) {
	if s.roomStore == nil {
		return nil, ErrSignedRoomUnavailable
	}
	if ownerUserID <= 0 {
		return nil, ErrInvalidRoomOwner
	}

	if err := s.maybeCleanupExpiredSignedRooms(ctx, time.Now().UTC()); err != nil {
		log.Printf("Best-effort signed room cleanup failed before list for owner %d: %v", ownerUserID, err)
	}
	return s.roomStore.ListOwnedSignedRooms(ctx, ownerUserID)
}

func (s *Service) HandleExtendSignedRoom(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName string, ttl time.Duration) (model.SignedRoom, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, ErrInvalidRoomName
	}
	if ownerUserID <= 0 {
		return model.SignedRoom{}, ErrInvalidRoomOwner
	}

	normalizedTTL, err := normalizeSignedRoomTTL(ttl)
	if err != nil {
		return model.SignedRoom{}, err
	}

	room, err := s.roomStore.GetSignedRoomByName(ctx, roomName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SignedRoom{}, ErrSignedRoomNotFound
		}
		return model.SignedRoom{}, err
	}

	now := time.Now().UTC()
	if room.OwnerUserID != ownerUserID {
		return model.SignedRoom{}, ErrRoomOwnedByAnotherUser
	}
	if !room.ExpiresAt.After(now) {
		if err := s.roomStore.DeleteSignedRoomByName(ctx, roomName); err != nil {
			log.Printf("Could not delete expired signed room before extend %s: %v", roomName, err)
		}
		return model.SignedRoom{}, ErrSignedRoomExpired
	}
	if err := s.maybeCleanupExpiredSignedRooms(ctx, now); err != nil {
		log.Printf("Best-effort signed room cleanup failed before extend for room %s: %v", roomName, err)
	}

	newExpiresAt := room.ExpiresAt.Add(normalizedTTL)
	if newExpiresAt.After(now.Add(MaxSignedRoomCapacity)) {
		return model.SignedRoom{}, ErrSignedRoomCapacityTooLarge
	}

	updated, err := s.roomStore.UpdateSignedRoomExpiry(ctx, roomName, ownerUserID, ownerDisplayName, room.EntryCode, newExpiresAt)
	if err != nil {
		return model.SignedRoom{}, err
	}

	s.recordRoomMembershipBestEffort(ctx, ownerUserID, roomName, roomHistoryRoleOwned)
	s.recordRoomMembershipBestEffort(ctx, ownerUserID, roomName, roomHistoryRoleJoined)

	return updated, nil
}

func (s *Service) HandleRecordSignedRoomJoin(ctx context.Context, roomName string, userID int64) error {
	if s.roomStore == nil {
		return ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return ErrInvalidRoomName
	}
	if userID <= 0 {
		return ErrInvalidRoomOwner
	}

	if err := s.roomStore.RecordRoomMembership(ctx, userID, roomName, roomHistoryRoleJoined); err != nil {
		return err
	}
	return s.roomStore.PruneRoomMemberships(ctx, userID, roomHistoryLimit)
}

func (s *Service) HandleListRoomHistory(ctx context.Context, userID int64) ([]model.RoomHistory, error) {
	if s.roomStore == nil {
		return nil, ErrSignedRoomUnavailable
	}
	if userID <= 0 {
		return nil, ErrInvalidRoomOwner
	}

	if err := s.maybeCleanupExpiredSignedRooms(ctx, time.Now().UTC()); err != nil {
		log.Printf("Best-effort signed room cleanup failed before history list for user %d: %v", userID, err)
	}
	return s.roomStore.ListRoomMemberships(ctx, userID, roomHistoryLimit)
}

func (s *Service) HandleReviveSignedRoom(ctx context.Context, roomName string, ownerUserID int64, ownerDisplayName string, ttl time.Duration) (model.SignedRoom, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, ErrInvalidRoomName
	}
	if ownerUserID <= 0 {
		return model.SignedRoom{}, ErrInvalidRoomOwner
	}

	if _, err := s.roomStore.GetRoomMembership(ctx, ownerUserID, roomName, roomHistoryRoleOwned); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SignedRoom{}, ErrSignedRoomNotFound
		}
		return model.SignedRoom{}, err
	}

	normalizedTTL, err := normalizeSignedRoomTTL(ttl)
	if err != nil {
		return model.SignedRoom{}, err
	}

	now := time.Now().UTC()
	if err := s.maybeCleanupExpiredSignedRooms(ctx, now); err != nil {
		log.Printf("Best-effort signed room cleanup failed before revive for room %s: %v", roomName, err)
	}

	existing, err := s.roomStore.GetSignedRoomByName(ctx, roomName)
	if err == nil {
		if existing.OwnerUserID != ownerUserID {
			return model.SignedRoom{}, ErrRoomOwnedByAnotherUser
		}
		if existing.ExpiresAt.After(now) {
			return model.SignedRoom{}, ErrSignedRoomAlreadyActive
		}
		if err := s.roomStore.DeleteSignedRoomByName(ctx, roomName); err != nil {
			log.Printf("Could not delete expired signed room before revive %s: %v", roomName, err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.SignedRoom{}, err
	}

	entryCode, err := generateRoomEntryCode()
	if err != nil {
		return model.SignedRoom{}, err
	}

	room, err := s.roomStore.CreateSignedRoom(ctx, roomName, ownerUserID, ownerDisplayName, entryCode, now.Add(normalizedTTL))
	if err != nil {
		return model.SignedRoom{}, err
	}

	s.recordRoomMembershipBestEffort(ctx, ownerUserID, roomName, roomHistoryRoleOwned)
	s.recordRoomMembershipBestEffort(ctx, ownerUserID, roomName, roomHistoryRoleJoined)

	return room, nil
}

func (s *Service) HandleDeleteSignedRoom(ctx context.Context, roomName string, ownerUserID int64) error {
	if s.roomStore == nil {
		return ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return ErrInvalidRoomName
	}
	if ownerUserID <= 0 {
		return ErrInvalidRoomOwner
	}

	room, err := s.roomStore.GetSignedRoomByName(ctx, roomName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSignedRoomNotFound
		}
		return err
	}

	now := time.Now().UTC()
	if !room.ExpiresAt.After(now) {
		if err := s.roomStore.DeleteSignedRoomByName(ctx, roomName); err != nil {
			log.Printf("Could not delete expired signed room %s: %v", roomName, err)
		}
		return ErrSignedRoomExpired
	}
	if room.OwnerUserID != ownerUserID {
		return ErrRoomOwnedByAnotherUser
	}

	return s.roomStore.DeleteSignedRoomByName(ctx, roomName)
}

func (s *Service) recordRoomMembershipBestEffort(ctx context.Context, userID int64, roomName, role string) {
	if err := s.roomStore.RecordRoomMembership(ctx, userID, roomName, role); err != nil {
		log.Printf("Could not record room membership user=%d room=%s role=%s: %v", userID, roomName, role, err)
		return
	}
	if err := s.roomStore.PruneRoomMemberships(ctx, userID, roomHistoryLimit); err != nil {
		log.Printf("Could not prune room history user=%d: %v", userID, err)
	}
}

func (s *Service) HandleGetSignedRoomStatus(ctx context.Context, roomName string) (model.SignedRoom, bool, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, false, ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, false, ErrInvalidRoomName
	}

	room, err := s.roomStore.GetSignedRoomByName(ctx, roomName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SignedRoom{}, false, nil
		}
		return model.SignedRoom{}, false, err
	}

	now := time.Now().UTC()
	if !room.ExpiresAt.After(now) {
		if err := s.roomStore.DeleteSignedRoomByName(ctx, roomName); err != nil {
			log.Printf("Could not delete expired signed room %s: %v", roomName, err)
		}
		return model.SignedRoom{}, false, ErrSignedRoomExpired
	}

	return room, true, nil
}

func normalizeSignedRoomTTL(ttl time.Duration) (time.Duration, error) {
	if ttl <= 0 {
		return DefaultSignedRoomTTL, nil
	}
	if ttl > MaxSignedRoomTTL {
		return 0, ErrSignedRoomTTLTooLarge
	}
	return ttl, nil
}

func normalizeRoomEntryCode(entryCode string) (string, error) {
	code := strings.TrimSpace(entryCode)
	if len(code) != SignedRoomCodeLength {
		return "", ErrInvalidRoomEntryCode
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return "", ErrInvalidRoomEntryCode
		}
	}
	return code, nil
}

func generateRoomEntryCode() (string, error) {
	minValue := model.SignedRoomEntryCodeMinValue()
	rangeSize := model.SignedRoomEntryCodeRangeSize()

	n, err := rand.Int(rand.Reader, big.NewInt(rangeSize))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", SignedRoomCodeLength, n.Int64()+minValue), nil
}

func (s *Service) maybeCleanupExpiredSignedRooms(ctx context.Context, now time.Time) error {
	if s.signedRoomCleanupEvery <= 0 {
		_, err := s.roomStore.DeleteExpiredSignedRooms(ctx, now)
		return err
	}

	s.cleanupMu.Lock()
	shouldRun := s.lastSignedRoomCleanup.IsZero() || now.Sub(s.lastSignedRoomCleanup) >= s.signedRoomCleanupEvery
	if shouldRun {
		s.lastSignedRoomCleanup = now
	}
	s.cleanupMu.Unlock()

	if !shouldRun {
		return nil
	}

	if _, err := s.roomStore.DeleteExpiredSignedRooms(ctx, now); err != nil {
		s.cleanupMu.Lock()
		s.lastSignedRoomCleanup = time.Time{}
		s.cleanupMu.Unlock()
		return err
	}
	return nil
}
