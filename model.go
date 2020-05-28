package gorm

import (
	"database/sql/driver"
	"time"
)

// Model base model definition, including fields `ID`, `CreatedAt`, `UpdatedAt`, `DeletedAt`, which could be embedded in your models
//    type User struct {
//      gorm.Model
//    }
type Model struct {
	ID        uint `gorm:"primary_key"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time `sql:"index"`
}

type primary struct {
	ID uint
}

func (x *primary) Scan(src interface{}) error {
	if val, ok := src.(uint); ok {
		x.ID = val
	} else if val, ok := src.(float64); ok {
		// This happens due to oracle driver not knowing that uint is required by the calling code.
		x.ID = uint(val)
	} else if val, ok := src.(int64); ok {
		x.ID = uint(val)
	}
	// Note: This else results in errors while inserting into the table in oracle. Hence not using such a clause
	//else {
	//	return errors.New(fmt.Sprintf("Unable to convert %v to uint", src))
	//}

	return nil
}

func (x *primary) Value() (driver.Value, error) {
	return x.ID, nil
}

type ORM struct {
	ID        primary `gorm:"primary_key"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time `sql:"index"`
}
