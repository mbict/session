package session

import (
	"context"
	"sync"
	"time"

	"github.com/bytedance/gopkg/collection/skipmap"
	"github.com/google/uuid"
)

var (
	_   ManagerStore = &memoryStore{}
	_   Store        = &store{}
	now              = time.Now
)

// Management of session storage, including creation, update, and delete operations
type ManagerStore interface {
	// Check the session store exists
	Check(ctx context.Context, sid string) (bool, error)
	// Create a session store and specify the expiration time (in seconds)
	Create(ctx context.Context, sid string, expired int64) (Store, error)
	// Update a session store and specify the expiration time (in seconds)
	Update(ctx context.Context, sid string, expired int64) (Store, error)
	// Delete a session store
	Delete(ctx context.Context, sid string) error
	// Use sid to replace old sid and return session store
	Refresh(ctx context.Context, oldsid, sid string, expired int64) (Store, error)
	// Close storage, release resources
	Close() error
}

// A session id storage operation
type Store interface {
	// Get a session storage context
	Context() context.Context
	// Get the current session id
	SessionID() string
	// Set session value, call save function to take effect
	Set(key string, value interface{})
	// Get session value
	Get(key string) (interface{}, bool)
	// GetString get session value as a string
	GetString(key string) (string, bool)
	// GetInt get session value as a integer
	GetInt(key string) (int, bool)
	// GetBool get session value as a boolean
	GetBool(key string) (bool, bool)
	// GetUUID get session value as a UUID
	GetUUID(key string) (uuid.UUID, bool)
	// Delete session value, call save function to take effect
	Delete(key string) interface{}
	// Save session data
	Save() error
	// Clear all session data
	Flush() error
}

// Create a new session storage (memory)
func NewMemoryStore() ManagerStore {
	mstore := &memoryStore{
		ticker: time.NewTicker(time.Second),
		data:   skipmap.NewString(),
	}

	go mstore.gc()
	return mstore
}

type dataItem struct {
	sid       string
	expiredAt time.Time
	values    map[string]interface{}
}

func newDataItem(sid string, values map[string]interface{}, expired int64) *dataItem {
	return &dataItem{
		sid:       sid,
		expiredAt: now().Add(time.Duration(expired) * time.Second),
		values:    values,
	}
}

type memoryStore struct {
	ticker *time.Ticker
	data   *skipmap.StringMap
}

func (s *memoryStore) gc() {
	for range s.ticker.C {
		s.data.Range(func(key string, value interface{}) bool {
			if item, ok := value.(*dataItem); ok && item.expiredAt.Before(now()) {
				s.data.Delete(key)
			}
			return true
		})
	}
}

func (s *memoryStore) save(sid string, values map[string]interface{}, expired int64) {
	if dt, ok := s.data.Load(sid); ok {
		dt.(*dataItem).values = values
		return
	}

	s.data.Store(sid, newDataItem(sid, values, expired))
}

func (s *memoryStore) Check(ctx context.Context, sid string) (bool, error) {
	dt, ok := s.data.Load(sid)
	if !ok {
		return false, nil
	}

	if item, ok := dt.(*dataItem); ok && item.expiredAt.After(now()) {
		return true, nil
	}
	return false, nil
}

func (s *memoryStore) Create(ctx context.Context, sid string, expired int64) (Store, error) {
	return newStore(ctx, s, sid, expired, nil), nil
}

func (s *memoryStore) Update(ctx context.Context, sid string, expired int64) (Store, error) {
	dt, ok := s.data.Load(sid)
	if !ok {
		return newStore(ctx, s, sid, expired, nil), nil
	}

	item := dt.(*dataItem)
	item.expiredAt = now().Add(time.Duration(expired) * time.Second)
	s.data.Store(sid, item)
	return newStore(ctx, s, sid, expired, item.values), nil
}

func (s *memoryStore) delete(sid string) {
	s.data.Delete(sid)
}

func (s *memoryStore) Delete(_ context.Context, sid string) error {
	s.delete(sid)
	return nil
}

func (s *memoryStore) Refresh(ctx context.Context, oldsid, sid string, expired int64) (Store, error) {
	dt, ok := s.data.Load(oldsid)
	if !ok {
		return newStore(ctx, s, sid, expired, nil), nil
	}

	item := dt.(*dataItem)
	newItem := newDataItem(sid, item.values, expired)
	s.data.Store(sid, newItem)
	s.delete(oldsid)
	return newStore(ctx, s, sid, expired, newItem.values), nil
}

func (s *memoryStore) Close() error {
	s.ticker.Stop()
	return nil
}

func newStore(ctx context.Context, mstore *memoryStore, sid string, expired int64, values map[string]interface{}) *store {
	if values == nil {
		values = make(map[string]interface{})
	}

	return &store{
		mstore:  mstore,
		ctx:     ctx,
		sid:     sid,
		expired: expired,
		values:  values,
	}
}

type store struct {
	sync.RWMutex
	mstore  *memoryStore
	ctx     context.Context
	sid     string
	expired int64
	values  map[string]interface{}
}

func (s *store) Context() context.Context {
	return s.ctx
}

func (s *store) SessionID() string {
	return s.sid
}

func (s *store) Set(key string, value interface{}) {
	s.Lock()
	s.values[key] = value
	s.Unlock()
}

func (s *store) Get(key string) (interface{}, bool) {
	s.RLock()
	val, ok := s.values[key]
	s.RUnlock()
	return val, ok
}

func (s *store) GetString(key string) (string, bool) {
	if v, ok := s.Get(key); ok {
		str, ok := v.(string)
		return str, ok
	}
	return "", false
}

func (s *store) GetBool(key string) (bool, bool) {
	if v, ok := s.Get(key); ok {
		b, ok := v.(bool)
		return b, ok
	}
	return false, false
}

func (s *store) GetInt(key string) (int, bool) {
	if v, ok := s.Get(key); ok {
		i, ok := v.(int)
		return i, ok
	}
	return 0, false
}

func (s *store) GetUUID(key string) (uuid.UUID, bool) {
	if v, ok := s.Get(key); ok {
		switch t := v.(type) {
		case uuid.UUID:
			return t, true
		case string:
			if id, err := uuid.Parse(t); err == nil {
				return id, true
			}
		case []byte:
			if id, err := uuid.FromBytes(t); err == nil {
				return id, true
			}
		}
	}
	return uuid.Nil, false
}

func (s *store) Delete(key string) interface{} {
	s.RLock()
	v, ok := s.values[key]
	s.RUnlock()

	if ok {
		s.Lock()
		delete(s.values, key)
		s.Unlock()
	}
	return v
}

func (s *store) Flush() error {
	s.Lock()
	s.values = make(map[string]interface{})
	s.Unlock()

	return s.Save()
}

func (s *store) Save() error {
	s.RLock()
	values := s.values
	s.RUnlock()

	s.mstore.save(s.sid, values, s.expired)
	return nil
}
