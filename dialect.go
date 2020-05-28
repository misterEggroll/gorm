package gorm

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Dialect interface contains behaviors that differ across SQL database
type Dialect interface {
	// GetName get dialect's name
	GetName() string

	// SetDB set db for dialect
	SetDB(db SQLCommon)

	// BindVar return the placeholder for actual values in SQL statements, in many dbs it is "?", Postgres using $1
	BindVar(i int) string
	// Quote quotes field name to avoid SQL parsing exceptions by using a reserved word as a field name
	Quote(key string) string
	// DataTypeOf return data's sql type
	DataTypeOf(field *StructField) string

	// HasIndex check has index or not
	HasIndex(tableName string, indexName string) bool
	// HasForeignKey check has foreign key or not
	HasForeignKey(tableName string, foreignKeyName string) bool
	// RemoveIndex remove index
	RemoveIndex(tableName string, indexName string) error
	// Remove constraint from a column in the DB
	RemoveConstraint(tableName string, constraintName string) error
	// HasTable check has table or not
	HasTable(tableName string) bool
	// HasColumn check has column or not
	HasColumn(tableName string, columnName string) bool
	// ModifyColumn modify column's type
	ModifyColumn(tableName string, columnName string, typ string) error

	// LimitAndOffsetSQL return generated SQL with Limit and Offset, as mssql has special case
	LimitAndOffsetSQL(limit, offset interface{}) string
	// SelectFromDummyTable return select values, for most dbs, `SELECT values` just works, mysql needs `SELECT value FROM DUAL`
	SelectFromDummyTable() string
	// LastInsertIdReturningSuffix most dbs support LastInsertId, but postgres needs to use `RETURNING`
	LastInsertIDReturningSuffix(tableName, columnName string) string
	// DefaultValueStr
	DefaultValueStr() string

	// BuildKeyName returns a valid key name (foreign key, index key) for the given table, field and reference
	BuildKeyName(kind, tableName string, fields ...string) string

	// NormalizeIndexAndColumn returns valid index name and column name depending on each dialect
	NormalizeIndexAndColumn(indexName, columnName string) (string, string)

	// CurrentDatabase return current database name
	CurrentDatabase() string

	// In case the database returns a row ID, it will resolve the row ID with the primary key of the row
	// Currently only supports a single primary key
	ResolveRowID(tableName string, rowID uint) uint

	// Separator at the end of a statement
	ClientStatementSeparator() string

	// Checks if a field name matches with a given column name. This is a special case for oracle DB since column names are all CAPS
	ColumnEquality(fieldDBName, columnName string) bool

	// Determing the tag setting based on the dialect being used
	GetTagSetting(field *StructField, key string) (string, bool)
	// Determine the limit of byte size for a BLOB
	GetByteLimit() int
}

var dialectsMap = map[string]Dialect{}

func newDialect(name string, db SQLCommon) Dialect {
	if value, ok := dialectsMap[name]; ok {
		dialect := reflect.New(reflect.TypeOf(value).Elem()).Interface().(Dialect)
		dialect.SetDB(db)
		return dialect
	}

	fmt.Printf("`%v` is not officially supported, running under compatibility mode.\n", name)
	commontDialect := &commonDialect{}
	commontDialect.SetDB(db)
	return commontDialect
}

// RegisterDialect register new dialect
func RegisterDialect(name string, dialect Dialect) {
	dialectsMap[name] = dialect
}

// GetDialect gets the dialect for the specified dialect name
func GetDialect(name string) (dialect Dialect, ok bool) {
	dialect, ok = dialectsMap[name]
	return
}

// ParseFieldStructForDialect get field's sql data type
var ParseFieldStructForDialect = func(field *StructField, dialect Dialect) (fieldValue reflect.Value, sqlType string, size int, additionalType string) {
	// Get redirected field type
	var (
		reflectType = field.Struct.Type
		dataType, _ = dialect.GetTagSetting(field, "TYPE")
	)

	for reflectType.Kind() == reflect.Ptr {
		reflectType = reflectType.Elem()
	}

	// Get redirected field value
	fieldValue = reflect.Indirect(reflect.New(reflectType))

	if gormDataType, ok := fieldValue.Interface().(interface {
		GormDataType(Dialect) string
	}); ok {
		dataType = gormDataType.GormDataType(dialect)
	}

	// Get scanner's real value
	if dataType == "" {
		var getScannerValue func(reflect.Value)
		getScannerValue = func(value reflect.Value) {
			fieldValue = value
			if _, isScanner := reflect.New(fieldValue.Type()).Interface().(sql.Scanner); isScanner && fieldValue.Kind() == reflect.Struct {
				getScannerValue(fieldValue.Field(0))
			}
		}
		getScannerValue(fieldValue)
	}

	// Default Size
	if num, ok := dialect.GetTagSetting(field, "SIZE"); ok {
		size, _ = strconv.Atoi(num)
	} else {
		size = 255
	}

	// Default type from tag setting
	notNull, _ := dialect.GetTagSetting(field, "NOT NULL")
	unique, _ := dialect.GetTagSetting(field, "UNIQUE")
	additionalType = notNull + " " + unique
	if value, ok := dialect.GetTagSetting(field, "DEFAULT"); ok {
		additionalType = additionalType + " DEFAULT " + value
	}

	if value, ok := dialect.GetTagSetting(field, "COMMENT"); ok {
		additionalType = additionalType + " COMMENT " + value
	}

	return fieldValue, dataType, size, strings.TrimSpace(additionalType)
}

func currentDatabaseAndTable(dialect Dialect, tableName string) (string, string) {
	if strings.Contains(tableName, ".") {
		splitStrings := strings.SplitN(tableName, ".", 2)
		return splitStrings[0], splitStrings[1]
	}
	return dialect.CurrentDatabase(), tableName
}
