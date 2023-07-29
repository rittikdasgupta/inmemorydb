package core

import (
	"errors"
	"inmemorydb/config"
	"sync"
	"time"

	"github.com/labstack/gommon/log"
)

type InMemoryDbValue struct {
	Value interface{}
	Expiry *time.Time
	Mu *sync.RWMutex
}

type InMemoryDb struct {
	Data map[string]InMemoryDbValue
	Queue []interface{}
	cmdMap map[string]func(Operation) (*InMemoryDbValue, error)
}

func (db *InMemoryDb) Set(query Operation) (*InMemoryDbValue, error) {
	done := make(chan int)
	defer func() {
		if err := recover(); err != nil {
			log.Infof("[InMemoryDb] Query timed out: %s", query.QueryString)
		}
		close(done)
	}()

	value := InMemoryDbValue{
		Value: *query.Value,
		Expiry: nil,
	}

	if query.Expiry != nil {
		exp := time.Now().Add(time.Second * time.Duration(*query.Expiry))
		value.Expiry = &exp
	}

	if val, ok := db.Data[*query.Key]; ok {
		val.Mu.Lock()
	}

	// Start timer
	ticker := time.NewTicker(config.KeyValuePairLockTimeout * time.Second)

	// Release mutex lock for key value pair after KeyValuePairLockTimeout second
	go func(){
		for {
			select {
			case <- done:
				return
			case <- ticker.C:
				if _, ok := db.Data[*query.Key]; ok {
					db.Data[*query.Key].Mu.Unlock()
				}
				ticker.Stop()
				panic("process timed out")
			}
		}
	}()

	time.Sleep(time.Second * 15)
	if query.Condition != nil {
		if *query.Condition == NX {
			// NX -> set key value pair only if key does not exist
			if _, ok := db.Data[*query.Key]; !ok {
				db.Data[*query.Key] = value
				return nil, nil
			}
		} else if *query.Condition == XX {
			// XX -> set key value pair only if key exists
			if _, ok := db.Data[*query.Key]; ok {
				db.Data[*query.Key] = value
				return nil, nil
			}
		}
	} else {
		db.Data[*query.Key] = value
	}

	return nil, nil
}

func (db *InMemoryDb) Get(query Operation) (*InMemoryDbValue, error) {
	isExp := db.isKeyExpired(*query.Key)

	if isExp {
		// Passive expired key removal: removes expired keys
		delete(db.Data, *query.Key)
		err := errors.New("key not found")
		return nil, err
	}

	value := db.Data[*query.Key]
	return &value, nil
}

func (db *InMemoryDb) isKeyExpired(key string) bool {
	if val, ok := db.Data[key]; ok {
		if val.Expiry == nil {
			return false
		}

		exp := (*val.Expiry).Unix() - time.Now().Unix()
		if exp > 0 {
			return false
		}
	}
	
	return true
}

func (db *InMemoryDb) Command(commandString string) (interface{}, error) {
	pr := NewCommandParser(commandString)
	pr.Parse()
	if err := pr.Err(); err != nil {
		return nil, err
	}

	// Check if command is valid
	if !pr.IsValid() {
		err := errors.New("invalid command")
		return nil, err
	}

	// Run query
	cmd := *pr.Query.Cmd
	dbResponse, err := db.cmdMap[cmd](pr.Query)
	if err != nil {
		return nil, err
	}

	log.Infof("[InMemoryDb] Query Executed %s", commandString)
	if dbResponse != nil {
		return dbResponse.Value, nil
	}
	return nil, nil
}

func StartInMemoryDb() *InMemoryDb {
	// Start go cron to auto delete expiry keys
	// go func(){
	// 	time.Sleep(10 * time.Second)
	// }()
	
	log.Infof("[InMemoryDb] Initiating startup...")
	db := &InMemoryDb{
		Data: map[string]InMemoryDbValue{},
		Queue: make([]interface{}, 0),
	}

	cmdMap := map[string]func(Operation) (*InMemoryDbValue, error) {
		"Set": db.Set,
		"Get": db.Get,	
	}

	db.cmdMap = cmdMap

	log.Infof("[InMemoryDb] started inmemory database")

	return db
}