package data

import (
	"database/sql"
	"errors"
)


var (
	ErrRecordNotFound  = errors.New("record not found")
	ErrEditConflict    = errors.New("edit conflict")
	ErrDuplicateRecord = errors.New("duplicate record")
	ErrInvalidSourceURL = errors.New("invalid source URL")
)


type Models struct {
	Collections CollectionModel
	Sources     SourceModel
	Users       UserModel
	Chunks      ChunkModel
}


func NewModels(db *sql.DB) Models {
	return Models{
		Collections: CollectionModel{DB: db},
		Sources:     SourceModel{DB: db},
		Users:       UserModel{DB: db},
		Chunks:      ChunkModel{DB: db},
	}
}
