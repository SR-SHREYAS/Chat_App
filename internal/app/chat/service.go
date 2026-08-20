package chat

import (
	"context"
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

	"github.com/gorilla/websocket"
	"github.com/lib/pq"
)

const (
	SocketBufferSize      = 1024
	MessageBufferSize     = 256
	DefaultSignedRoomTTL  = 10 * time.Minute
	MaxSignedRoomTTL      = 7 * 24 * time.Hour
	MaxSignedRoomCapacity = 10 * 24 * time.Hour
	SignedRoomCodeLength  = 4
	signedRoomCleanupTTL  = 1 * time.Minute
	roomHistoryLimit      = 15
	entryCodeRetryLimit   = 25
	roomHistoryRoleOwner  = "owner"
	roomHistoryRoleMember = "member"
)

var (
	ErrSignedRoomUnavailable            = errors.New("signed room service unavailable")
	ErrInvalidRoomName                  = errors.New("invalid room name")
	ErrInvalidRoomOwner                 = errors.New("invalid room owner")
	ErrSignedRoomTTLTooLarge            = errors.New("signed room ttl exceeds maximum")
	ErrSignedRoomNotFound               = errors.New("signed room not found")
	ErrSignedRoomExpired                = errors.New("signed room expired")
	ErrSignedRoomAuthenticationRequired = errors.New("sign-in required for this room")
	ErrSignedRoomAlreadyActive          = errors.New("signed room already active")
	ErrSignedRoomCapacityTooLarge       = errors.New("signed room active capacity exceeds maximum")
	ErrRoomOwnedByAnotherUser           = errors.New("room is owned by another user")
	ErrInvalidRoomEntryCode             = errors.New("invalid room entry code")
	ErrInvalidRoomEntryCodeFormat       = errors.New("invalid entry code format")
	ErrInvalidRoomCredentials           = errors.New("invalid room name or entry code")
	ErrOwnerUserIDRequired              = errors.New("owner user id is required")
	ErrRoomIDRequired                   = errors.New("room_id is required")
	ErrSignedRoomNotFoundOrExpired      = errors.New("signed room not found or already expired")
	ErrRoomEntryCodeUnavailable         = errors.New("room entry code unavailable")
)

// OperationError preserves a client-safe operation message while retaining the
// underlying failure for logs and error inspection. It has no transport details.
type OperationError struct {
	Message string
	Cause   error
}

func (e *OperationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *OperationError) Unwrap() error {
	return e.Cause
}

type MessageRepository interface {
	SaveMessage(ctx context.Context, roomID, senderUserID, message string) error
	GetRecentMessages(ctx context.Context, roomID string, limit int) ([]model.Message, error)
	DeleteRoomMessages(ctx context.Context, roomID string) error
	Ping(ctx context.Context) error
}

type SignedRoomRepository interface {
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
	rooms                *Registry
	messageRepository    MessageRepository
	signedRoomRepository SignedRoomRepository

	cleanupMu              sync.Mutex
	lastSignedRoomCleanup  time.Time
	signedRoomCleanupEvery time.Duration
}

// RoomJoin holds the application state prepared before a WebSocket upgrade.
// A signed room's entry code arrives only after the upgrade, so Complete
// finishes the same join workflow once the handler receives that handshake.
type RoomJoin struct {
	service       *Service
	roomID        string
	roomKey       string
	signedRoom    model.SignedRoom
	hasSignedRoom bool
}

func NewService(messageRepository MessageRepository, signedRoomRepository SignedRoomRepository) *Service {
	return &Service{
		rooms:                  NewRegistry(),
		messageRepository:      messageRepository,
		signedRoomRepository:   signedRoomRepository,
		signedRoomCleanupEvery: signedRoomCleanupTTL,
	}
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
		messages: s.messageRepository,
		persist:  persistMessages,
	}
}

func (s *Service) sendRecentMessages(ctx context.Context, c *Client) {
	if !c.persist {
		return
	}

	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	messages, err := s.messageRepository.GetRecentMessages(queryCtx, c.room.name, 50)
	if err != nil {
		log.Printf("[HISTORY ERROR] Could not query recent messages for room ID %s: %v", c.room.name, err)
		return
	}

	log.Printf("[HISTORY DEBUG] Retrieved %d messages from DB for room %s", len(messages), c.room.name)

	count := 0
	for _, m := range messages {
		msgJSON, err := json.Marshal(map[string]string{
			"name":    m.Username,
			"message": m.Message,
		})
		if err != nil {
			log.Printf("[HISTORY ERROR] Error marshaling message for user %s: %v", m.Username, err)
			continue
		}
		c.receive <- msgJSON
		count++
	}

	log.Printf("[HISTORY DEBUG] Sent %d recent messages to %s in room %s", count, c.name, c.room.name)
}

func (s *Service) HandleRoom(ctx context.Context, socket *websocket.Conn, roomID, userID, username string, persistMessages bool) (*Room, *Client) {
	room := s.rooms.GetOrCreate(roomID)
	client := s.newClient(socket, room, userID, username, persistMessages)
	s.sendRecentMessages(ctx, client)
	return room, client
}

// HandleJoinRoom prepares the application workflow for joining a room.
func (s *Service) HandleJoinRoom(ctx context.Context, roomID, roomKey string, authenticated bool) (*RoomJoin, error) {
	join := &RoomJoin{
		service: s,
		roomID:  roomID,
		roomKey: roomKey,
	}

	if roomID == "" {
		return join, nil
	}

	signedRoom, exists, err := s.HandleGetSignedRoomStatus(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrSignedRoomNotFound
	}
	if !authenticated {
		return nil, ErrSignedRoomAuthenticationRequired
	}

	join.signedRoom = signedRoom
	join.hasSignedRoom = true
	return join, nil
}

func (j *RoomJoin) RequiresSignedRoomHandshake() bool {
	return j.hasSignedRoom
}

// Complete finishes a prepared join after the transport layer receives any
// signed-room entry-code handshake.
func (j *RoomJoin) Complete(ctx context.Context, socket *websocket.Conn, userID, username, entryCode string, afterSignedRoomJoin func()) (*Room, *Client, error) {
	if j.hasSignedRoom {
		if entryCode != j.signedRoom.EntryCode {
			return nil, nil, ErrInvalidRoomEntryCode
		}
		if err := j.service.HandleRecordSignedRoomJoin(ctx, j.roomID, userID); err != nil {
			log.Printf("Could not record signed room join for user=%s room=%s: %v", userID, j.roomID, err)
		}
		if afterSignedRoomJoin != nil {
			afterSignedRoomJoin()
		}
	}

	room, client := j.service.HandleRoom(ctx, socket, j.roomKey, userID, username, j.hasSignedRoom)
	return room, client, nil
}

func (s *Service) HandleHealth(ctx context.Context) error {
	return s.messageRepository.Ping(ctx)
}

func (s *Service) HandleCreateSignedRoom(ctx context.Context, roomName string, ownerUserID string, ttl time.Duration) (model.SignedRoom, error) {
	if s.signedRoomRepository == nil {
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

		room, err = s.signedRoomRepository.CreateSignedRoom(ctx, roomName, ownerUserID, entryCode, expiresAt)
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

func (s *Service) HandleJoinSignedRoom(ctx context.Context, roomName, entryCode string) (model.SignedRoom, error) {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return model.SignedRoom{}, ErrInvalidRoomName
	}

	entryCode = strings.TrimSpace(entryCode)
	if entryCode == "" || !isValidRoomEntryCode(entryCode) {
		return model.SignedRoom{}, ErrInvalidRoomEntryCodeFormat
	}

	room, err := s.signedRoomRepository.GetSignedRoomByNameAndCode(ctx, roomName, entryCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SignedRoom{}, ErrInvalidRoomCredentials
		}
		return model.SignedRoom{}, &OperationError{Message: "database error", Cause: err}
	}

	now := time.Now().UTC()
	if !room.ExpiresAt.After(now) {
		return model.SignedRoom{}, ErrSignedRoomExpired
	}

	return room, nil
}

func (s *Service) HandleListOwnedSignedRooms(ctx context.Context, ownerUserID string) ([]model.SignedRoom, error) {
	if s.signedRoomRepository == nil {
		return nil, ErrSignedRoomUnavailable
	}
	if ownerUserID == "" {
		return nil, ErrInvalidRoomOwner
	}

	if err := s.maybeDeleteExpiredSignedRooms(ctx, time.Now().UTC()); err != nil {
		log.Printf("Best-effort signed room cleanup failed before list for owner %s: %v", ownerUserID, err)
	}
	return s.signedRoomRepository.ListOwnedSignedRooms(ctx, ownerUserID)
}

func (s *Service) HandleExtendSignedRoom(ctx context.Context, roomID string, ownerUserID string, ttl time.Duration) (model.SignedRoom, error) {
	if s.signedRoomRepository == nil {
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

	room, err := s.signedRoomRepository.GetSignedRoomByID(ctx, roomID)
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

	updated, err := s.signedRoomRepository.UpdateSignedRoomExpiry(ctx, roomID, ownerUserID, room.EntryCode, newExpiresAt)
	if err != nil {
		return model.SignedRoom{}, err
	}

	s.recordRoomMembershipBestEffort(ctx, ownerUserID, roomID, roomHistoryRoleOwner)

	return updated, nil
}

func (s *Service) HandleRecordSignedRoomJoin(ctx context.Context, roomID string, userID string) error {
	if s.signedRoomRepository == nil {
		return ErrSignedRoomUnavailable
	}

	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return ErrInvalidRoomName
	}
	if userID == "" {
		return ErrInvalidRoomOwner
	}

	if err := s.signedRoomRepository.RecordRoomMembership(ctx, userID, roomID, roomHistoryRoleMember); err != nil {
		return err
	}
	return s.signedRoomRepository.PruneRoomMemberships(ctx, userID, roomHistoryLimit)
}

func (s *Service) HandleListRoomHistory(ctx context.Context, userID string) ([]model.RoomHistory, error) {
	if s.signedRoomRepository == nil {
		return nil, ErrSignedRoomUnavailable
	}
	if userID == "" {
		return nil, ErrInvalidRoomOwner
	}

	if err := s.maybeDeleteExpiredSignedRooms(ctx, time.Now().UTC()); err != nil {
		log.Printf("Best-effort signed room cleanup failed before history list for user %s: %v", userID, err)
	}
	return s.signedRoomRepository.ListRoomMemberships(ctx, userID, roomHistoryLimit)
}

func (s *Service) HandleReviveSignedRoom(ctx context.Context, roomID string, ownerUserID string, ttl time.Duration) (model.SignedRoom, error) {
	if s.signedRoomRepository == nil {
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
	existing, err := s.signedRoomRepository.GetSignedRoomByID(ctx, roomID)
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

		room, err = s.signedRoomRepository.UpdateSignedRoomExpiry(ctx, roomID, ownerUserID, entryCode, now.Add(normalizedTTL))
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
	if s.signedRoomRepository == nil {
		return ErrSignedRoomUnavailable
	}

	roomID = strings.TrimSpace(roomID)
	ownerUserID = strings.TrimSpace(ownerUserID)

	if roomID == "" {
		return ErrInvalidRoomName
	}
	if ownerUserID == "" {
		return ErrOwnerUserIDRequired
	}

	err := s.signedRoomRepository.ExpireSignedRoom(ctx, roomID, ownerUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSignedRoomNotFoundOrExpired
		}
		return &OperationError{Message: "failed to delete signed room", Cause: err}
	}

	// Actively kick out any connected clients (like guests) so they can't keep chatting
	if room := s.rooms.GetOrCreate(roomID); room != nil {
		msgJSON, _ := json.Marshal(map[string]string{
			"type":    "error",
			"code":    "room_deleted",
			"message": "This room has been deleted by the owner.",
		})
		select {
		case room.forward <- msgJSON:
		default:
		}
	}

	return nil
}

// HandlePurgeSignedRoom performs a "hard delete", permanently removing the room
// and all its associated data from the database.
func (s *Service) HandlePurgeSignedRoom(ctx context.Context, roomID string, ownerUserID string) error {
	roomID = strings.TrimSpace(roomID)
	ownerUserID = strings.TrimSpace(ownerUserID)

	if roomID == "" {
		return ErrRoomIDRequired
	}
	if ownerUserID == "" {
		return ErrOwnerUserIDRequired
	}
	if s.signedRoomRepository == nil {
		return ErrSignedRoomUnavailable
	}
	room, err := s.signedRoomRepository.GetSignedRoomByID(ctx, roomID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSignedRoomNotFound
		}
		return &OperationError{Message: "database error", Cause: err}
	}
	if room.OwnerUserID != ownerUserID {
		return ErrRoomOwnedByAnotherUser
	}
	if err := s.signedRoomRepository.DeleteSignedRoomByID(ctx, roomID); err != nil {
		return &OperationError{Message: "database error", Cause: err}
	}

	// Actively kick out any connected clients
	if room := s.rooms.GetOrCreate(roomID); room != nil {
		msgJSON, _ := json.Marshal(map[string]string{
			"type":    "error",
			"code":    "room_deleted",
			"message": "This room has been permanently deleted.",
		})
		select {
		case room.forward <- msgJSON:
		default:
		}
	}

	// Best-effort: message deletion errors are logged inside deleteRoomMessagesBestEffort
	// but do not change the purge outcome.
	s.deleteRoomMessagesBestEffort(ctx, roomID)
	return nil
}

func (s *Service) recordRoomMembershipBestEffort(ctx context.Context, userID string, roomID, role string) {
	if err := s.signedRoomRepository.RecordRoomMembership(ctx, userID, roomID, role); err != nil {
		log.Printf("Could not record room membership user=%s room=%s role=%s: %v", userID, roomID, role, err)
		return
	}
	if err := s.signedRoomRepository.PruneRoomMemberships(ctx, userID, roomHistoryLimit); err != nil {
		log.Printf("Could not prune room history user=%s: %v", userID, err)
	}
}

func (s *Service) HandleGetSignedRoomStatus(ctx context.Context, roomID string) (model.SignedRoom, bool, error) {
	if s.signedRoomRepository == nil {
		return model.SignedRoom{}, false, ErrSignedRoomUnavailable
	}

	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return model.SignedRoom{}, false, ErrInvalidRoomName
	}

	room, err := s.signedRoomRepository.GetSignedRoomByID(ctx, roomID)
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
	minValue := signedRoomEntryCodeMinValue()
	rangeSize := minValue * 9

	n, err := rand.Int(rand.Reader, big.NewInt(rangeSize))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", SignedRoomCodeLength, n.Int64()+minValue), nil
}

func signedRoomEntryCodeMinValue() int64 {
	minValue := int64(1)
	for i := 1; i < SignedRoomCodeLength; i++ {
		minValue *= 10
	}
	return minValue
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
		expiredRoomIDs, err := s.signedRoomRepository.DeleteExpiredSignedRooms(ctx, gracePeriodCutoff)
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

	expiredRoomIDs, err := s.signedRoomRepository.DeleteExpiredSignedRooms(ctx, gracePeriodCutoff)
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
	if s.messageRepository == nil {
		return
	}

	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return
	}

	deleteCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := s.messageRepository.DeleteRoomMessages(deleteCtx, roomID); err != nil {
		log.Printf("Could not delete messages for signed room %s: %v", roomID, err)
	}
}
