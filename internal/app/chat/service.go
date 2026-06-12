package chat

import (
	"context"
	"net/http"
	"strings"

	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	"real_time_chat_app/internal/model"
	"real_time_chat_app/internal/util"

	"github.com/gorilla/websocket"
	"github.com/lib/pq"
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
	entryCodeRetryLimit   = 25
	roomHistoryRoleOwner  = "owner"
	roomHistoryRoleMember = "member"
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
	ErrRoomEntryCodeUnavailable   = errors.New("room entry code unavailable")
)

type MessageStore interface {
	SaveMessage(ctx context.Context, roomID, senderUserID, message string) error
	GetRecentMessages(ctx context.Context, roomID string, limit int) ([]model.Message, error)
	DeleteRoomMessages(ctx context.Context, roomID string) error
	Ping(ctx context.Context) error
}

type SignedRoomStore interface {
	CreateSignedRoom(ctx context.Context, roomName string, ownerUserID string, entryCode string, expiresAt time.Time) (model.SignedRoom, error)
	GetSignedRoomByID(ctx context.Context, roomID string) (model.SignedRoom, error)
	GetSignedRoomByNameAndCode(ctx context.Context, roomName string, entryCode string) (model.SignedRoom, error)
	UpdateSignedRoomExpiry(ctx context.Context, roomID string, ownerUserID string, entryCode string, expiresAt time.Time) (model.SignedRoom, error)
	ListOwnedSignedRooms(ctx context.Context, ownerUserID string) ([]model.SignedRoom, error)
	DeleteSignedRoomByID(ctx context.Context, roomID string) error
	ExpireSignedRoom(ctx context.Context, roomID string, ownerUserID string) error
	DeleteExpiredSignedRooms(ctx context.Context, now time.Time) ([]string, error)
	RecordRoomMembership(ctx context.Context, userID string, roomID string, role string) error
	GetRoomMembership(ctx context.Context, userID string, roomID string) (model.RoomHistory, error)
	PruneRoomMemberships(ctx context.Context, userID string, limit int) error
	ListRoomMemberships(ctx context.Context, userID string, limit int) ([]model.RoomHistory, error)
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

func (s *Service) newClient(socket *websocket.Conn, room *Room, userID, userName string, persistMessages bool) *Client {
	name := strings.TrimSpace(userName)
	if name == "" {
		name = fmt.Sprintf("user-%s", userID)
	}

	return &Client{
		socket:   socket,
		room:     room,
		receive:  make(chan []byte, MessageBufferSize),
		userID:   userID,
		name:     name,
		messages: s.store,
		persist:  persistMessages,
	}
}

func (s *Service) sendRecentMessages(ctx context.Context, c *Client) {
	if !c.persist {
		return
	}

	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	messages, err := s.store.GetRecentMessages(queryCtx, c.room.name, 50)
	if err != nil {
		log.Printf("Could not query recent messages for room ID %s: %v", c.room.name, err)
		return
	}

	count := 0
	for _, m := range messages {
		msgJSON, err := json.Marshal(map[string]string{
			"name":    m.Username,
			"message": m.Message,
		})
		if err != nil {
			log.Printf("Error marshaling message for user %s: %v", m.Username, err)
			continue
		}
		c.receive <- msgJSON
		count++
	}

	log.Printf("Sent %d recent messages to %s in room %s", count, c.name, c.room.name)
}

func (s *Service) HandleRoom(ctx context.Context, socket *websocket.Conn, roomID, userID, username string, persistMessages bool) (*Room, *Client) {
	room := s.rooms.GetOrCreate(roomID)
	client := s.newClient(socket, room, userID, username, persistMessages)
	s.sendRecentMessages(ctx, client)
	return room, client
}

func (s *Service) HandleHealth(ctx context.Context) error {
	return s.store.Ping(ctx)
}

func (s *Service) HandleCreateSignedRoom(ctx context.Context, roomName string, ownerUserID string, ttl time.Duration) (model.SignedRoom, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, ErrSignedRoomUnavailable
	}

	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, ErrInvalidRoomName
	}
	if ownerUserID == "" {
		return model.SignedRoom{}, ErrInvalidRoomOwner
	}

	normalizedTTL, err := normalizeSignedRoomTTL(ttl)
	if err != nil {
		return model.SignedRoom{}, err
	}

	now := time.Now().UTC()
	if err := s.maybeDeleteExpiredSignedRooms(ctx, now); err != nil {
		log.Printf("Best-effort signed room cleanup failed before create for room %s: %v", roomName, err)
	}

	expiresAt := now.Add(normalizedTTL)
	var room model.SignedRoom
	for attempt := 0; attempt < entryCodeRetryLimit; attempt++ {
		entryCode, err := generateRoomEntryCode()
		if err != nil {
			return model.SignedRoom{}, err
		}

		room, err = s.roomStore.CreateSignedRoom(ctx, roomName, ownerUserID, entryCode, expiresAt)
		if err == nil {
			break
		}
		if !isUniqueConstraintViolation(err) {
			return model.SignedRoom{}, err
		}
	}
	if room.ID == "" {
		return model.SignedRoom{}, ErrRoomEntryCodeUnavailable
	}

	s.recordRoomMembershipBestEffort(ctx, ownerUserID, room.ID, roomHistoryRoleOwner)

	return room, nil
}

func (s *Service) JoinSignedRoom(ctx context.Context, roomName, entryCode string) (model.SignedRoom, error) {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, util.NewAppError(http.StatusBadRequest, "invalid room name", nil)
	}

	entryCode = strings.TrimSpace(entryCode)
	if entryCode == "" || !isValidRoomEntryCode(entryCode) {
		return model.SignedRoom{}, util.NewAppError(http.StatusBadRequest, "invalid entry code format", nil)
	}

	room, err := s.roomStore.GetSignedRoomByNameAndCode(ctx, roomName, entryCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SignedRoom{}, util.NewAppError(http.StatusNotFound, "invalid room name or entry code", nil)
		}
		return model.SignedRoom{}, util.NewAppError(http.StatusInternalServerError, "database error", err)
	}

	now := time.Now().UTC()
	if !room.ExpiresAt.After(now) {
		return model.SignedRoom{}, util.NewAppError(http.StatusGone, "signed room expired", nil)
	}

	return room, nil
}

func (s *Service) HandleListOwnedSignedRooms(ctx context.Context, ownerUserID string) ([]model.SignedRoom, error) {
	if s.roomStore == nil {
		return nil, ErrSignedRoomUnavailable
	}
	if ownerUserID == "" {
		return nil, ErrInvalidRoomOwner
	}

	if err := s.maybeDeleteExpiredSignedRooms(ctx, time.Now().UTC()); err != nil {
		log.Printf("Best-effort signed room cleanup failed before list for owner %s: %v", ownerUserID, err)
	}
	return s.roomStore.ListOwnedSignedRooms(ctx, ownerUserID)
}

func (s *Service) HandleExtendSignedRoom(ctx context.Context, roomID string, ownerUserID string, ttl time.Duration) (model.SignedRoom, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, ErrSignedRoomUnavailable
	}

	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return model.SignedRoom{}, ErrInvalidRoomName
	}
	if ownerUserID == "" {
		return model.SignedRoom{}, ErrInvalidRoomOwner
	}

	normalizedTTL, err := normalizeSignedRoomTTL(ttl)
	if err != nil {
		return model.SignedRoom{}, err
	}

	room, err := s.roomStore.GetSignedRoomByID(ctx, roomID)
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
		return model.SignedRoom{}, ErrSignedRoomExpired
	}
	if err := s.maybeDeleteExpiredSignedRooms(ctx, now); err != nil {
		log.Printf("Best-effort signed room cleanup failed before extend for room %s: %v", roomID, err)
	}

	newExpiresAt := room.ExpiresAt.Add(normalizedTTL)
	if newExpiresAt.After(now.Add(MaxSignedRoomCapacity)) {
		return model.SignedRoom{}, ErrSignedRoomCapacityTooLarge
	}

	updated, err := s.roomStore.UpdateSignedRoomExpiry(ctx, roomID, ownerUserID, room.EntryCode, newExpiresAt)
	if err != nil {
		return model.SignedRoom{}, err
	}

	s.recordRoomMembershipBestEffort(ctx, ownerUserID, roomID, roomHistoryRoleOwner)

	return updated, nil
}

func (s *Service) HandleRecordSignedRoomJoin(ctx context.Context, roomID string, userID string) error {
	if s.roomStore == nil {
		return ErrSignedRoomUnavailable
	}

	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return ErrInvalidRoomName
	}
	if userID == "" {
		return ErrInvalidRoomOwner
	}

	if err := s.roomStore.RecordRoomMembership(ctx, userID, roomID, roomHistoryRoleMember); err != nil {
		return err
	}
	return s.roomStore.PruneRoomMemberships(ctx, userID, roomHistoryLimit)
}

func (s *Service) HandleListRoomHistory(ctx context.Context, userID string) ([]model.RoomHistory, error) {
	if s.roomStore == nil {
		return nil, ErrSignedRoomUnavailable
	}
	if userID == "" {
		return nil, ErrInvalidRoomOwner
	}

	if err := s.maybeDeleteExpiredSignedRooms(ctx, time.Now().UTC()); err != nil {
		log.Printf("Best-effort signed room cleanup failed before history list for user %s: %v", userID, err)
	}
	return s.roomStore.ListRoomMemberships(ctx, userID, roomHistoryLimit)
}

func (s *Service) HandleReviveSignedRoom(ctx context.Context, roomID string, ownerUserID string, ttl time.Duration) (model.SignedRoom, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, ErrSignedRoomUnavailable
	}

	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return model.SignedRoom{}, ErrInvalidRoomName
	}
	if ownerUserID == "" {
		return model.SignedRoom{}, ErrInvalidRoomOwner
	}

	normalizedTTL, err := normalizeSignedRoomTTL(ttl)
	if err != nil {
		return model.SignedRoom{}, err
	}

	now := time.Now().UTC()
	existing, err := s.roomStore.GetSignedRoomByID(ctx, roomID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SignedRoom{}, ErrSignedRoomNotFound
		}
		return model.SignedRoom{}, err
	}
	if existing.OwnerUserID != ownerUserID {
		return model.SignedRoom{}, ErrRoomOwnedByAnotherUser
	}
	if existing.ExpiresAt.After(now) {
		return model.SignedRoom{}, ErrSignedRoomAlreadyActive
	}

	var room model.SignedRoom
	for attempt := 0; attempt < entryCodeRetryLimit; attempt++ {
		entryCode, err := generateRoomEntryCode()
		if err != nil {
			return model.SignedRoom{}, err
		}

		room, err = s.roomStore.UpdateSignedRoomExpiry(ctx, roomID, ownerUserID, entryCode, now.Add(normalizedTTL))
		if err == nil {
			break
		}
		if !isUniqueConstraintViolation(err) {
			return model.SignedRoom{}, err
		}
	}
	if room.ID == "" {
		return model.SignedRoom{}, ErrRoomEntryCodeUnavailable
	}

	if err := s.maybeDeleteExpiredSignedRooms(ctx, now); err != nil {
		log.Printf("Best-effort signed room cleanup failed after revive for room %s: %v", roomID, err)
	}

	return room, nil
}

// HandleDeleteSignedRoom performs a "soft delete" by expiring the room,
// putting it into a 7-day grace period where it can be revived or purged.
func (s *Service) HandleDeleteSignedRoom(ctx context.Context, roomID string, ownerUserID string) error {
	if s.roomStore == nil {
		return util.NewAppError(http.StatusServiceUnavailable, ErrSignedRoomUnavailable.Error(), nil)
	}

	roomID = strings.TrimSpace(roomID)
	ownerUserID = strings.TrimSpace(ownerUserID)

	if roomID == "" {
		return util.NewAppError(http.StatusBadRequest, ErrInvalidRoomName.Error(), nil)
	}
	if ownerUserID == "" {
		return util.NewAppError(http.StatusBadRequest, "owner user id is required", nil)
	}

	err := s.roomStore.ExpireSignedRoom(ctx, roomID, ownerUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return util.NewAppError(http.StatusNotFound, "signed room not found or already expired", nil)
		}
		return util.NewAppError(http.StatusInternalServerError, "failed to delete signed room", err)
	}

	return nil
}

// HandlePurgeSignedRoom performs a "hard delete", permanently removing the room
// and all its associated data from the database.
func (s *Service) HandlePurgeSignedRoom(ctx context.Context, roomID string, ownerUserID string) error {
	roomID = strings.TrimSpace(roomID)
	ownerUserID = strings.TrimSpace(ownerUserID)

	if roomID == "" {
		return util.NewAppError(http.StatusBadRequest, "room_id is required", nil)
	}
	if ownerUserID == "" {
		return util.NewAppError(http.StatusBadRequest, "owner user id is required", nil)
	}
	if s.roomStore == nil {
		return util.NewAppError(http.StatusServiceUnavailable, ErrSignedRoomUnavailable.Error(), nil)
	}
	room, err := s.roomStore.GetSignedRoomByID(ctx, roomID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return util.NewAppError(http.StatusNotFound, ErrSignedRoomNotFound.Error(), err)
		}
		return util.NewAppError(http.StatusInternalServerError, "database error", err)
	}
	if room.OwnerUserID != ownerUserID {
		return util.NewAppError(http.StatusForbidden, ErrRoomOwnedByAnotherUser.Error(), nil)
	}
	if err := s.roomStore.DeleteSignedRoomByID(ctx, roomID); err != nil {
		return util.NewAppError(http.StatusInternalServerError, "database error", err)
	}
	// Best-effort: message deletion errors are logged inside deleteRoomMessagesBestEffort
	// but do not change the purge outcome.
	s.deleteRoomMessagesBestEffort(ctx, roomID)
	return nil
}

func (s *Service) recordRoomMembershipBestEffort(ctx context.Context, userID string, roomID, role string) {
	if err := s.roomStore.RecordRoomMembership(ctx, userID, roomID, role); err != nil {
		log.Printf("Could not record room membership user=%s room=%s role=%s: %v", userID, roomID, role, err)
		return
	}
	if err := s.roomStore.PruneRoomMemberships(ctx, userID, roomHistoryLimit); err != nil {
		log.Printf("Could not prune room history user=%s: %v", userID, err)
	}
}

func (s *Service) HandleGetSignedRoomStatus(ctx context.Context, roomID string) (model.SignedRoom, bool, error) {
	if s.roomStore == nil {
		return model.SignedRoom{}, false, ErrSignedRoomUnavailable
	}

	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return model.SignedRoom{}, false, ErrInvalidRoomName
	}

	room, err := s.roomStore.GetSignedRoomByID(ctx, roomID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SignedRoom{}, false, nil
		}
		return model.SignedRoom{}, false, err
	}

	now := time.Now().UTC()
	if !room.ExpiresAt.After(now) {
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

func isValidRoomEntryCode(code string) bool {
	if len(code) != SignedRoomCodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
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

func isUniqueConstraintViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

func (s *Service) maybeDeleteExpiredSignedRooms(ctx context.Context, now time.Time) error {
	// 7-Day Grace Period: Only physically delete rooms that have been expired for over 7 days.
	// This gives owners a window to "Revive" them with their message history intact!
	gracePeriodCutoff := now.Add(-7 * 24 * time.Hour)

	if s.signedRoomCleanupEvery <= 0 {
		expiredRoomIDs, err := s.roomStore.DeleteExpiredSignedRooms(ctx, gracePeriodCutoff)
		if err != nil {
			return err
		}
		s.deleteRoomMessagesForRooms(ctx, expiredRoomIDs)
		return nil
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

	expiredRoomIDs, err := s.roomStore.DeleteExpiredSignedRooms(ctx, gracePeriodCutoff)
	if err != nil {
		s.cleanupMu.Lock()
		s.lastSignedRoomCleanup = time.Time{}
		s.cleanupMu.Unlock()
		return err
	}
	s.deleteRoomMessagesForRooms(ctx, expiredRoomIDs)
	return nil
}

func (s *Service) deleteRoomMessagesForRooms(ctx context.Context, roomIDs []string) {
	for _, roomID := range roomIDs {
		s.deleteRoomMessagesBestEffort(ctx, roomID)
	}
}

func (s *Service) deleteRoomMessagesBestEffort(ctx context.Context, roomID string) {
	if s.store == nil {
		return
	}

	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return
	}

	deleteCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := s.store.DeleteRoomMessages(deleteCtx, roomID); err != nil {
		log.Printf("Could not delete messages for signed room %s: %v", roomID, err)
	}
}
