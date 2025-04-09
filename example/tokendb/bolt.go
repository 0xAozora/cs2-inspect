package tokendb

import (
	"sync"

	bolt "go.etcd.io/bbolt"
)

type TokenDB struct {
	db *bolt.DB
	m  sync.Mutex
}

func NewTokenDB(name string) (*TokenDB, error) {

	db, err := bolt.Open(name, 0666, nil)
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("tokens"))
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &TokenDB{db: db}, nil
}

func (t *TokenDB) GetToken(name string) (string, error) {

	var token string

	t.m.Lock()
	_ = t.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("tokens"))
		token = string(b.Get([]byte(name)))
		return nil
	})
	t.m.Unlock()
	return token, nil
}

func (t *TokenDB) SetToken(name, token string) error {
	t.m.Lock()
	_ = t.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("tokens"))
		if token != string(b.Get([]byte(name))) {
			_ = b.Put([]byte(name), []byte(token))
		}
		return nil
	})
	t.m.Unlock()
	return nil
}
