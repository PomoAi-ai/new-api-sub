// Package logstore keeps the logs synced from new-api pools in MongoDB, for 30
// days, and reads them as a newapi.LogSource.
package logstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"newapisub/internal/newapi"
)

// RetentionSeconds is how long a synced log stays: the TTL of the ts index.
const RetentionSeconds = 30 * 86400

// connectTimeout bounds connecting and finding the server: a request that
// reads synced logs waits this long at most when MongoDB is down.
const connectTimeout = 3 * time.Second

// Store is the logs collection, shared by every pool.
type Store struct {
	client *mongo.Client
	logs   *mongo.Collection
}

// Open connects to uri, pings and makes sure of the indexes. Its errors never
// quote uri: it holds the password.
func Open(ctx context.Context, uri, db string) (*Store, error) {
	opts := options.Client().ApplyURI(uri).SetServerSelectionTimeout(connectTimeout).SetConnectTimeout(connectTimeout)
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, errors.New("open mongo: MONGO_URI is not a valid connection string")
	}
	s := &Store{client: client, logs: client.Database(db).Collection("logs")}
	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(context.Background())
		return nil, fmt.Errorf("ping mongo: %w", err)
	}
	if err := s.ensureIndexes(ctx); err != nil {
		client.Disconnect(context.Background())
		return nil, err
	}
	return s, nil
}

func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

func (s *Store) ensureIndexes(ctx context.Context) error {
	asc, desc := 1, -1
	_, err := s.logs.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "pool_id", Value: asc}, {Key: "log_id", Value: asc}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "pool_id", Value: asc}, {Key: "created_at", Value: desc}, {Key: "log_id", Value: desc}}},
		{Keys: bson.D{{Key: "pool_id", Value: asc}, {Key: "channel_id", Value: asc}, {Key: "created_at", Value: desc}}},
		{Keys: bson.D{{Key: "request_id", Value: asc}},
			Options: options.Index().SetPartialFilterExpression(bson.D{{Key: "request_id", Value: bson.D{{Key: "$gt", Value: ""}}}})},
		{Keys: bson.D{{Key: "ts", Value: asc}}, Options: options.Index().SetExpireAfterSeconds(RetentionSeconds)},
	})
	if err != nil {
		return fmt.Errorf("create mongo log indexes: %w", err)
	}
	return nil
}

// doc is a log as stored. content and other are masked; cache_read and
// cache_write are what the SQL reads of other, read once on the way in.
type doc struct {
	PoolID            int64     `bson:"pool_id"`
	LogID             int64     `bson:"log_id"`
	FP                string    `bson:"fp"`
	CreatedAt         int64     `bson:"created_at"`
	TS                time.Time `bson:"ts"`
	Type              int64     `bson:"type"`
	ChannelID         int64     `bson:"channel_id"` // the pool's own id
	ChannelName       string    `bson:"channel_name"`
	ModelName         string    `bson:"model_name"`
	Quota             int64     `bson:"quota"`
	PromptTokens      int64     `bson:"prompt_tokens"`
	CompletionTokens  int64     `bson:"completion_tokens"`
	UseTime           int64     `bson:"use_time"`
	IsStream          bool      `bson:"is_stream"`
	Content           string    `bson:"content"`
	Other             string    `bson:"other"`
	CacheRead         int64     `bson:"cache_read"`
	CacheWrite        int64     `bson:"cache_write"`
	UserID            int64     `bson:"user_id"`
	Username          string    `bson:"username"`
	TokenID           int64     `bson:"token_id"`
	TokenName         string    `bson:"token_name"`
	IP                string    `bson:"ip"`
	Group             string    `bson:"group"`
	RequestID         string    `bson:"request_id"`
	UpstreamRequestID string    `bson:"upstream_request_id"`
	SyncedAt          time.Time `bson:"synced_at"`
}

// stored are the log types the SQL reads; the sync keeps no other.
func stored(typ int) bool { return typ == 2 || typ == 5 || typ == 6 }

// Fingerprint identifies what a log says, to tell a log id new-api reused.
func Fingerprint(l newapi.Log) string {
	b, _ := json.Marshal([]any{l.CreatedAt, l.Type, l.ChannelID, l.ModelName, l.Quota, l.PromptTokens,
		l.CompletionTokens, l.RequestID, l.UserID, l.TokenID}) // cannot fail on these types
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var (
	otherCacheRead  = otherIntRe("cache_tokens")
	otherCacheWrite = otherIntRe("cache_write_tokens")
)

// otherIntRe is newapi's otherInt as PostgreSQL runs it, on the raw text.
func otherIntRe(field string) *regexp.Regexp {
	return regexp.MustCompile(`"` + field + `":[[:space:]]*([0-9]{1,12})(?:[^0-9]|$)`)
}

func otherInt(re *regexp.Regexp, other string) int64 {
	m := re.FindStringSubmatch(other)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseInt(m[1], 10, 64) // at most 12 digits
	return n
}

func toDoc(poolID int64, l newapi.Log, now time.Time) doc {
	return doc{
		PoolID: poolID, LogID: l.ID, FP: Fingerprint(l), CreatedAt: l.CreatedAt, TS: time.Unix(l.CreatedAt, 0),
		Type: int64(l.Type), ChannelID: int64(l.ChannelID), ChannelName: l.ChannelName, ModelName: l.ModelName,
		Quota: l.Quota, PromptTokens: l.PromptTokens, CompletionTokens: l.CompletionTokens, UseTime: l.UseTime,
		IsStream: l.IsStream, Content: newapi.MaskKeys(l.Content), Other: newapi.MaskJSON(l.Other),
		CacheRead: otherInt(otherCacheRead, l.Other), CacheWrite: otherInt(otherCacheWrite, l.Other),
		UserID: l.UserID, Username: l.Username, TokenID: l.TokenID, TokenName: l.TokenName, IP: l.IP, Group: l.Group,
		RequestID: l.RequestID, UpstreamRequestID: l.UpstreamRequestID, SyncedAt: now,
	}
}

func (d doc) log() newapi.Log {
	return newapi.Log{
		ID: d.LogID, CreatedAt: d.CreatedAt, Type: int(d.Type), ChannelID: int(d.ChannelID), ChannelName: d.ChannelName,
		ModelName: d.ModelName, Quota: d.Quota, PromptTokens: d.PromptTokens, CompletionTokens: d.CompletionTokens,
		UseTime: d.UseTime, IsStream: d.IsStream, Content: d.Content, UserID: d.UserID, Username: d.Username,
		TokenID: d.TokenID, TokenName: d.TokenName, IP: d.IP, Group: d.Group, RequestID: d.RequestID,
		UpstreamRequestID: d.UpstreamRequestID, Other: d.Other,
	}
}

// Conflict is a log whose id the pool already has with another fingerprint.
// The stored log stays as it is.
type Conflict struct {
	LogID     int64  `json:"-"`
	CreatedAt int64  `json:"created_at"`
	RequestID string `json:"request_id"`
	FP        string `json:"fp"`
	StoredFP  string `json:"stored_fp"`
}

const dupKey = 11000

// Insert stores the logs of a pool it does not have yet; a log it has with the
// same fingerprint changes nothing, so a window read twice stores nothing
// twice. Only types 2, 5 and 6 are kept. inserted counts the new logs.
func (s *Store) Insert(ctx context.Context, poolID int64, logs []newapi.Log) (inserted int, conflicts []Conflict, err error) {
	now := time.Now()
	var models []mongo.WriteModel
	var docs []doc
	for _, l := range logs {
		if !stored(l.Type) {
			continue
		}
		d := toDoc(poolID, l, now)
		docs = append(docs, d)
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "pool_id", Value: poolID}, {Key: "log_id", Value: d.LogID}, {Key: "fp", Value: d.FP}}).
			SetUpdate(bson.D{{Key: "$setOnInsert", Value: d}}).SetUpsert(true))
	}
	if len(models) == 0 {
		return 0, nil, nil
	}
	res, err := s.logs.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if res != nil {
		inserted = int(res.UpsertedCount)
	}
	var bwe mongo.BulkWriteException
	if err != nil && (!errors.As(err, &bwe) || bwe.WriteConcernError != nil) {
		return inserted, nil, fmt.Errorf("insert mongo logs of pool %d: %w", poolID, err)
	}
	var dup []doc
	for _, we := range bwe.WriteErrors {
		if we.Code != dupKey {
			return inserted, nil, fmt.Errorf("insert mongo logs of pool %d: %w", poolID, err)
		}
		dup = append(dup, docs[we.Index])
	}
	if len(dup) == 0 {
		return inserted, nil, nil
	}
	conflicts, err = s.conflicts(ctx, poolID, dup)
	return inserted, conflicts, err
}

// conflicts reads the stored fingerprints of the logs an insert found taken.
// One equal to the log's own was a concurrent insert of the same log.
func (s *Store) conflicts(ctx context.Context, poolID int64, dup []doc) ([]Conflict, error) {
	ids := make([]int64, len(dup))
	for i, d := range dup {
		ids[i] = d.LogID
	}
	cur, err := s.logs.Find(ctx, bson.D{{Key: "pool_id", Value: poolID}, {Key: "log_id", Value: bson.D{{Key: "$in", Value: ids}}}},
		options.Find().SetProjection(bson.D{{Key: "log_id", Value: 1}, {Key: "fp", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("read mongo fingerprints of pool %d: %w", poolID, err)
	}
	var have []struct {
		LogID int64  `bson:"log_id"`
		FP    string `bson:"fp"`
	}
	if err := cur.All(ctx, &have); err != nil {
		return nil, fmt.Errorf("read mongo fingerprints of pool %d: %w", poolID, err)
	}
	fps := map[int64]string{}
	for _, h := range have {
		fps[h.LogID] = h.FP
	}
	var out []Conflict
	for _, d := range dup {
		if fp, ok := fps[d.LogID]; ok && fp != d.FP {
			out = append(out, Conflict{LogID: d.LogID, CreatedAt: d.CreatedAt, RequestID: d.RequestID, FP: d.FP, StoredFP: fp})
		}
	}
	return out, nil
}

// Duplicate is a log another pool has with a request id of this one's.
type Duplicate struct {
	PoolID    int64  `json:"other_pool_id"`
	LogID     int64  `json:"other_log_id"`
	RequestID string `json:"request_id"`
}

// OtherPool finds a log of a pool other than poolID with one of requestIDs:
// the same new-api configured as two pools.
func (s *Store) OtherPool(ctx context.Context, poolID int64, requestIDs []string) (Duplicate, bool, error) {
	var d Duplicate
	if len(requestIDs) == 0 {
		return d, false, nil
	}
	var got struct {
		PoolID    int64  `bson:"pool_id"`
		LogID     int64  `bson:"log_id"`
		RequestID string `bson:"request_id"`
	}
	err := s.logs.FindOne(ctx, bson.D{{Key: "request_id", Value: bson.D{{Key: "$in", Value: requestIDs}}},
		{Key: "pool_id", Value: bson.D{{Key: "$ne", Value: poolID}}}}).Decode(&got)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return d, false, nil
	}
	if err != nil {
		return d, false, fmt.Errorf("find mongo logs by request id: %w", err)
	}
	return Duplicate{got.PoolID, got.LogID, got.RequestID}, true, nil
}

// DropPool deletes every synced log of a pool, for a sync started over.
func (s *Store) DropPool(ctx context.Context, poolID int64) (int64, error) {
	res, err := s.logs.DeleteMany(ctx, bson.D{{Key: "pool_id", Value: poolID}})
	if err != nil {
		return 0, fmt.Errorf("delete mongo logs of pool %d: %w", poolID, err)
	}
	return res.DeletedCount, nil
}
