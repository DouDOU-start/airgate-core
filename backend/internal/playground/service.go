package playground

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	sdk "github.com/DouDOU-start/airgate-sdk"
)

type ServiceOptions struct {
	DefaultGroupID     int
	MaxConversations   int
	MaxContextMessages int
	Storage            *ObjectStorage
}

type Service struct {
	logger  *slog.Logger
	db      *sql.DB
	host    sdk.Host
	opts    ServiceOptions
	storage *ObjectStorage
}

func NewService(logger *slog.Logger, db *sql.DB, host sdk.Host, opts ServiceOptions) *Service {
	if opts.DefaultGroupID <= 0 {
		opts.DefaultGroupID = 1
	}
	if opts.MaxContextMessages <= 0 {
		opts.MaxContextMessages = 50
	}
	return &Service{logger: logger, db: db, host: host, opts: opts, storage: opts.Storage}
}

// ── Conversation CRUD ──

func (s *Service) CreateConversation(ctx context.Context, userID int, title string, groupID int64, platform, model string) (*Conversation, error) {
	if s.opts.MaxConversations > 0 {
		var count int
		if err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM playground_conversations WHERE user_id = $1", userID,
		).Scan(&count); err != nil {
			return nil, fmt.Errorf("count conversations: %w", err)
		}
		if count >= s.opts.MaxConversations {
			return nil, fmt.Errorf("maximum conversations (%d) reached", s.opts.MaxConversations)
		}
	}

	conv := &Conversation{UserID: userID, Title: title, GroupID: groupID, Platform: platform, Model: model}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO playground_conversations (user_id, title, group_id, platform, model)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_at, updated_at`,
		userID, title, groupID, platform, model,
	).Scan(&conv.ID, &conv.CreatedAt, &conv.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert conversation: %w", err)
	}
	return conv, nil
}

func (s *Service) ListConversations(ctx context.Context, userID int) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, title, group_id, platform, model, created_at, updated_at
		 FROM playground_conversations
		 WHERE user_id = $1
		 ORDER BY updated_at DESC
		 LIMIT 200`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var convs []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.GroupID, &c.Platform, &c.Model, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}

func (s *Service) GetConversation(ctx context.Context, userID int, convID int64) (*Conversation, error) {
	c := &Conversation{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, title, group_id, platform, model, created_at, updated_at
		 FROM playground_conversations
		 WHERE id = $1 AND user_id = $2`, convID, userID,
	).Scan(&c.ID, &c.UserID, &c.Title, &c.GroupID, &c.Platform, &c.Model, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (s *Service) UpdateConversation(ctx context.Context, userID int, convID int64, title string, groupID int64, platform, model string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE playground_conversations
		 SET title = $1, group_id = $2, platform = $3, model = $4, updated_at = NOW()
		 WHERE id = $5 AND user_id = $6`,
		title, groupID, platform, model, convID, userID,
	)
	return err
}

func (s *Service) DeleteConversation(ctx context.Context, userID int, convID int64) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM playground_conversations WHERE id = $1 AND user_id = $2",
		convID, userID,
	)
	return err
}

// ── Messages ──

func (s *Service) ListMessages(ctx context.Context, userID int, convID int64) ([]Message, error) {
	var ownerID int
	if err := s.db.QueryRowContext(ctx,
		"SELECT user_id FROM playground_conversations WHERE id = $1", convID,
	).Scan(&ownerID); err != nil {
		return nil, err
	}
	if ownerID != userID {
		return nil, fmt.Errorf("conversation not found")
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, reasoning, reasoning_effort, platform, model, group_id, input_tokens, output_tokens, cost, created_at
		 FROM playground_messages
		 WHERE conversation_id = $1
		 ORDER BY created_at`, convID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Reasoning, &m.ReasoningEffort, &m.Platform, &m.Model, &m.GroupID, &m.InputTokens, &m.OutputTokens, &m.Cost, &m.CreatedAt); err != nil {
			return nil, err
		}
		if resolved, err := s.resolveAssetURLs(ctx, userID, m.Content); err != nil {
			s.logger.Warn("failed to resolve message assets", "error", err, "message_id", m.ID)
		} else {
			m.Content = resolved
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *Service) saveMessage(ctx context.Context, convID int64, role, content, reasoning, reasoningEffort, platform, model string, groupID int64, inputTokens, outputTokens int, cost float64) (*Message, error) {
	m := &Message{
		ConversationID:  convID,
		Role:            role,
		Content:         content,
		Reasoning:       reasoning,
		ReasoningEffort: reasoningEffort,
		Platform:        platform,
		Model:           model,
		GroupID:         groupID,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		Cost:            cost,
	}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO playground_messages (conversation_id, role, content, reasoning, reasoning_effort, platform, model, group_id, input_tokens, output_tokens, cost)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id, created_at`,
		convID, role, content, reasoning, reasoningEffort, platform, model, groupID, inputTokens, outputTokens, cost,
	).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx, "UPDATE playground_conversations SET updated_at = NOW() WHERE id = $1", convID); err != nil {
		s.logger.Error("failed to refresh conversation timestamp", "error", err, "conv_id", convID)
	}
	return m, nil
}

type PersistMessageRequest struct {
	ConversationID  int64   `json:"conversation_id"`
	Role            string  `json:"role"`
	Content         string  `json:"content"`
	Reasoning       string  `json:"reasoning"`
	ReasoningEffort string  `json:"reasoning_effort"`
	Platform        string  `json:"platform"`
	Model           string  `json:"model"`
	GroupID         int64   `json:"group_id"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	Cost            float64 `json:"cost"`
}

type UpdateMessageRequest struct {
	Content      string  `json:"content"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

func (s *Service) UpdateMessage(ctx context.Context, userID int, msgID int64, req UpdateMessageRequest) (*Message, error) {
	if msgID <= 0 {
		return nil, fmt.Errorf("message_id required")
	}

	var msg Message
	var convUserID int
	if err := s.db.QueryRowContext(ctx,
		`SELECT m.id, m.conversation_id, m.role, m.content, m.reasoning, m.reasoning_effort, m.platform, m.model, m.group_id, m.input_tokens, m.output_tokens, m.cost, m.created_at, c.user_id
		 FROM playground_messages m
		 JOIN playground_conversations c ON c.id = m.conversation_id
		 WHERE m.id = $1`, msgID,
	).Scan(&msg.ID, &msg.ConversationID, &msg.Role, &msg.Content, &msg.Reasoning, &msg.ReasoningEffort, &msg.Platform, &msg.Model, &msg.GroupID, &msg.InputTokens, &msg.OutputTokens, &msg.Cost, &msg.CreatedAt, &convUserID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message not found")
		}
		return nil, fmt.Errorf("get message: %w", err)
	}
	if convUserID != userID {
		return nil, fmt.Errorf("message not found")
	}

	content, err := s.storeContentAssets(ctx, userID, msg.ConversationID, req.Content)
	if err != nil {
		return nil, fmt.Errorf("store message assets: %w", err)
	}
	msg.Content = content
	msg.InputTokens += req.InputTokens
	msg.OutputTokens += req.OutputTokens
	msg.Cost += req.Cost

	if _, err := s.db.ExecContext(ctx,
		`UPDATE playground_messages
		 SET content = $1, input_tokens = $2, output_tokens = $3, cost = $4
		 WHERE id = $5`,
		msg.Content, msg.InputTokens, msg.OutputTokens, msg.Cost, msg.ID,
	); err != nil {
		return nil, fmt.Errorf("update message: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE playground_conversations SET updated_at = NOW() WHERE id = $1", msg.ConversationID); err != nil {
		s.logger.Error("failed to refresh conversation timestamp", "error", err, "conv_id", msg.ConversationID)
	}
	if resolved, err := s.resolveAssetURLs(ctx, userID, msg.Content); err != nil {
		s.logger.Warn("failed to resolve updated message assets", "error", err, "message_id", msg.ID)
	} else {
		msg.Content = resolved
	}
	return &msg, nil
}

func (s *Service) PersistMessage(ctx context.Context, userID int, req PersistMessageRequest) (*Message, error) {
	if req.ConversationID <= 0 {
		return nil, fmt.Errorf("conversation_id required")
	}
	if req.Role != "user" && req.Role != "assistant" && req.Role != "system" {
		return nil, fmt.Errorf("invalid role")
	}

	conv, err := s.GetConversation(ctx, userID, req.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if conv == nil {
		return nil, fmt.Errorf("conversation not found")
	}

	groupID := req.GroupID
	if groupID <= 0 {
		groupID = conv.GroupID
	}
	platform := req.Platform
	if platform == "" {
		platform = conv.Platform
	}
	model := req.Model
	if model == "" {
		model = conv.Model
	}

	content, err := s.storeContentAssets(ctx, userID, conv.ID, req.Content)
	if err != nil {
		return nil, fmt.Errorf("store message assets: %w", err)
	}

	msg, err := s.saveMessage(ctx, conv.ID, req.Role, content, req.Reasoning, req.ReasoningEffort, platform, model, groupID, req.InputTokens, req.OutputTokens, req.Cost)
	if err != nil {
		return nil, fmt.Errorf("save message: %w", err)
	}
	if resolved, err := s.resolveAssetURLs(ctx, userID, msg.Content); err != nil {
		s.logger.Warn("failed to resolve persisted message assets", "error", err, "message_id", msg.ID)
	} else {
		msg.Content = resolved
	}

	if conv.Title == "" && req.Role == "assistant" {
		titleSeed, titleErr := s.latestUserMessageContent(ctx, conv.ID)
		if titleErr != nil {
			s.logger.Error("failed to query title seed", "error", titleErr, "conv_id", conv.ID)
		} else if titleSeed != "" {
			if err := s.UpdateConversation(ctx, userID, conv.ID, generateTitle(titleSeed), conv.GroupID, conv.Platform, conv.Model); err != nil {
				s.logger.Error("failed to update conversation title", "error", err, "conv_id", conv.ID)
			}
		}
	}

	return msg, nil
}

func (s *Service) storeContentAssets(ctx context.Context, userID int, convID int64, content string) (string, error) {
	if s.storage == nil || !strings.Contains(content, "data:image/") {
		return content, nil
	}
	var firstErr error
	stored := dataImageURLRE.ReplaceAllStringFunc(content, func(dataURL string) string {
		asset, err := s.storage.StoreImageDataURL(ctx, userID, convID, dataURL)
		if err != nil {
			firstErr = err
			return dataURL
		}
		if err := s.insertAsset(ctx, userID, convID, asset); err != nil {
			firstErr = err
			return dataURL
		}
		return assetURI(asset.ID)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return stored, nil
}

func (s *Service) insertAsset(ctx context.Context, userID int, convID int64, asset *StoredAsset) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO playground_assets (id, user_id, conversation_id, object_key, content_type, size_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		asset.ID, userID, convID, asset.ObjectKey, asset.ContentType, asset.SizeBytes,
	)
	return err
}

func (s *Service) resolveAssetURLs(ctx context.Context, userID int, content string) (string, error) {
	if s.storage == nil || !strings.Contains(content, "airgate-asset://") {
		return content, nil
	}
	var firstErr error
	assetLinkRE := regexp.MustCompile(`airgate-asset://asset/[A-Za-z0-9]+`)
	resolved := assetLinkRE.ReplaceAllStringFunc(content, func(raw string) string {
		id, ok := parseAssetURI(raw)
		if !ok {
			return raw
		}
		asset, err := s.getAsset(ctx, userID, id)
		if err != nil {
			firstErr = err
			return raw
		}
		publicURL, err := s.storage.PublicURL(ctx, asset.ObjectKey)
		if err != nil {
			firstErr = err
			return raw
		}
		return publicURL
	})
	return resolved, firstErr
}

func (s *Service) getAsset(ctx context.Context, userID int, id string) (*Asset, error) {
	asset := &Asset{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, conversation_id, object_key, content_type, size_bytes, created_at
		 FROM playground_assets
		 WHERE id = $1 AND user_id = $2`, id, userID,
	).Scan(&asset.ID, &asset.UserID, &asset.ConversationID, &asset.ObjectKey, &asset.ContentType, &asset.SizeBytes, &asset.CreatedAt)
	return asset, err
}

func (s *Service) latestUserMessageContent(ctx context.Context, convID int64) (string, error) {
	var content string
	err := s.db.QueryRowContext(ctx,
		`SELECT content FROM playground_messages
		 WHERE conversation_id = $1 AND role = 'user'
		 ORDER BY created_at DESC
		 LIMIT 1`, convID,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(content), nil
}

func generateTitle(userMessage string) string {
	runes := []rune(userMessage)
	if len(runes) > 30 {
		return string(runes[:30]) + "..."
	}
	return userMessage
}
