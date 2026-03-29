// contrib/mongo/history.go
package mongo

import (
	"context"
	"errors"
	"time"

	"github.com/axonframework/axon/interfaces"
	"github.com/axonframework/axon/kernel"
	"go.mongodb.org/mongo-driver/v2/bson"
	gomongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// historyDoc is the MongoDB document structure for a conversation session.
type historyDoc struct {
	SessionID string       `bson:"session_id"`
	Messages  []messageDoc `bson:"messages"`
	UpdatedAt time.Time    `bson:"updated_at"`
}

// messageDoc is the BSON representation of a kernel.Message.
type messageDoc struct {
	Role     string           `bson:"role"`
	Content  []contentPartDoc `bson:"content"`
	Metadata map[string]any   `bson:"metadata,omitempty"`
}

// contentPartDoc is the BSON representation of a kernel.ContentPart.
type contentPartDoc struct {
	Text       *string        `bson:"text,omitempty"`
	Image      *imageDoc      `bson:"image,omitempty"`
	ToolCall   *toolCallDoc   `bson:"tool_call,omitempty"`
	ToolResult *toolResultDoc `bson:"tool_result,omitempty"`
}

type imageDoc struct {
	URL      string `bson:"url"`
	MimeType string `bson:"mime_type,omitempty"`
}

type toolCallDoc struct {
	ID     string `bson:"id"`
	Name   string `bson:"name"`
	Params []byte `bson:"params,omitempty"`
}

type toolResultDoc struct {
	ToolCallID string `bson:"tool_call_id"`
	Name       string `bson:"name"`
	Content    string `bson:"content"`
	IsError    bool   `bson:"is_error,omitempty"`
}

// historyStore is a MongoDB-backed implementation of interfaces.HistoryStore.
// Each session is stored as a single document with a growing messages array.
type historyStore struct {
	coll *gomongo.Collection
}

// NewHistoryStore creates a MongoDB-backed HistoryStore.
// The collection should have a unique index on {session_id: 1}.
func NewHistoryStore(db *gomongo.Database, collection string) interfaces.HistoryStore {
	return &historyStore{
		coll: db.Collection(collection),
	}
}

// SaveMessages appends messages to the given session's history using
// $push with upsert. If the session document does not exist, it is created.
func (s *historyStore) SaveMessages(ctx context.Context, sessionID string, messages []kernel.Message) error {
	if len(messages) == 0 {
		return nil
	}

	docs := make([]messageDoc, len(messages))
	for i, msg := range messages {
		docs[i] = toMessageDoc(msg)
	}

	filter := bson.D{{Key: "session_id", Value: sessionID}}
	update := bson.D{
		{Key: "$push", Value: bson.D{
			{Key: "messages", Value: bson.D{
				{Key: "$each", Value: docs},
			}},
		}},
		{Key: "$set", Value: bson.D{
			{Key: "updated_at", Value: time.Now()},
		}},
	}

	opts := options.UpdateOne().SetUpsert(true)
	_, err := s.coll.UpdateOne(ctx, filter, update, opts)
	return err
}

// LoadMessages returns the last `limit` messages for the given session.
// If limit <= 0, all messages are returned.
func (s *historyStore) LoadMessages(ctx context.Context, sessionID string, limit int) ([]kernel.Message, error) {
	filter := bson.D{{Key: "session_id", Value: sessionID}}

	var findOpts *options.FindOneOptionsBuilder
	if limit > 0 {
		findOpts = options.FindOne().SetProjection(bson.D{
			{Key: "messages", Value: bson.D{{Key: "$slice", Value: -limit}}},
		})
	}

	var doc historyDoc
	var err error
	if findOpts != nil {
		err = s.coll.FindOne(ctx, filter, findOpts).Decode(&doc)
	} else {
		err = s.coll.FindOne(ctx, filter).Decode(&doc)
	}
	if err != nil {
		if errors.Is(err, gomongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}

	messages := make([]kernel.Message, len(doc.Messages))
	for i, md := range doc.Messages {
		messages[i] = fromMessageDoc(md)
	}
	return messages, nil
}

// Clear removes all messages for the given session by deleting the document.
func (s *historyStore) Clear(ctx context.Context, sessionID string) error {
	filter := bson.D{{Key: "session_id", Value: sessionID}}
	_, err := s.coll.DeleteOne(ctx, filter)
	return err
}

// toMessageDoc converts a kernel.Message to a BSON-friendly messageDoc.
func toMessageDoc(msg kernel.Message) messageDoc {
	doc := messageDoc{
		Role:     string(msg.Role),
		Content:  make([]contentPartDoc, len(msg.Content)),
		Metadata: msg.Metadata,
	}
	for i, part := range msg.Content {
		doc.Content[i] = toContentPartDoc(part)
	}
	return doc
}

func toContentPartDoc(part kernel.ContentPart) contentPartDoc {
	doc := contentPartDoc{
		Text: part.Text,
	}
	if part.Image != nil {
		doc.Image = &imageDoc{
			URL:      part.Image.URL,
			MimeType: part.Image.MimeType,
		}
	}
	if part.ToolCall != nil {
		doc.ToolCall = &toolCallDoc{
			ID:     part.ToolCall.ID,
			Name:   part.ToolCall.Name,
			Params: []byte(part.ToolCall.Params),
		}
	}
	if part.ToolResult != nil {
		doc.ToolResult = &toolResultDoc{
			ToolCallID: part.ToolResult.ToolCallID,
			Name:       part.ToolResult.Name,
			Content:    part.ToolResult.Content,
			IsError:    part.ToolResult.IsError,
		}
	}
	return doc
}

// fromMessageDoc converts a BSON messageDoc back to a kernel.Message.
func fromMessageDoc(doc messageDoc) kernel.Message {
	msg := kernel.Message{
		Role:     kernel.Role(doc.Role),
		Content:  make([]kernel.ContentPart, len(doc.Content)),
		Metadata: doc.Metadata,
	}
	for i, part := range doc.Content {
		msg.Content[i] = fromContentPartDoc(part)
	}
	return msg
}

func fromContentPartDoc(doc contentPartDoc) kernel.ContentPart {
	part := kernel.ContentPart{
		Text: doc.Text,
	}
	if doc.Image != nil {
		part.Image = &kernel.ImageContent{
			URL:      doc.Image.URL,
			MimeType: doc.Image.MimeType,
		}
	}
	if doc.ToolCall != nil {
		part.ToolCall = &kernel.ToolCall{
			ID:     doc.ToolCall.ID,
			Name:   doc.ToolCall.Name,
			Params: doc.ToolCall.Params,
		}
	}
	if doc.ToolResult != nil {
		part.ToolResult = &kernel.ToolResult{
			ToolCallID: doc.ToolResult.ToolCallID,
			Name:       doc.ToolResult.Name,
			Content:    doc.ToolResult.Content,
			IsError:    doc.ToolResult.IsError,
		}
	}
	return part
}
