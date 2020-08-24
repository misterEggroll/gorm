// +build oracle

package gorm

import (
	"crypto/sha1"
	"fmt"
	ociDriver "github.com/mattn/go-oci8"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type oci8 struct {
	commonDialect
}

func init() {
	RegisterDialect("oci8", new(oci8))
}

func (*oci8) GetName() string {
	return "oci8"
}

func (o *oci8) Quote(key string) string {
	// oracle only support names with a maximum of 30 characters
	key = o.buildSha(key)
	return fmt.Sprintf(`"%s"`, strings.ToUpper(key))
}

func (*oci8) SelectFromDummyTable() string {
	return "FROM DUAL"
}

func (*oci8) BindVar(i int) string {
	return fmt.Sprintf(":%d", i)
}

func (o *oci8) DataTypeOf(field *StructField) string {
	var sqlType, additionalType = o.SplitDataTypeOf(field)

	if len(strings.TrimSpace(additionalType)) == 0 {
		return sqlType
	}
	return fmt.Sprintf("%v %v", sqlType, additionalType)
}

func (o *oci8) SplitDataTypeOf(field *StructField) (string, string) {
	var dataValue, sqlType, size, additionalType = ParseFieldStructForDialect(field, o)

	charset, _ := o.GetTagSetting(field, "CHARSET")
	var strDataType string
	if strings.EqualFold(charset, "utf-8") {
		strDataType = "NVARCHAR2"
	} else {
		strDataType = "VARCHAR2"
	}

	if len(sqlType) == 0 {
		switch dataValue.Kind() {
		case reflect.Bool:
			sqlType = "CHAR(1)"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uintptr:
			if o.fieldCanAutoIncrement(field) {
				field.TagSettingsSet("AUTO_INCREMENT", "GENERATED ALWAYS")
				sqlType = "NUMBER GENERATED ALWAYS AS IDENTITY"
			} else {
				sqlType = "NUMBER"
			}
		case reflect.Int64, reflect.Uint64:
			if _, ok := field.TagSettings["AUTO_INCREMENT"]; ok || field.IsPrimaryKey {
				field.TagSettings["SEQUENCE"] = "SEQUENCE"
			}
			sqlType = "NUMBER"
		case reflect.Float32, reflect.Float64:
			sqlType = "FLOAT"
		case reflect.String:
			// Maximum size of VARCHAR2 is 4000 bytes or characters if MAX_STRING_SIZE = STANDARD
			if size > 0 && size < 4000 {
				sqlType = fmt.Sprintf("%s(%d)", strDataType, size)
			} else {
				sqlType = fmt.Sprintf("%s(255)", strDataType)
			}
		case reflect.Struct:
			if _, ok := dataValue.Interface().(time.Time); ok {
				sqlType = "TIMESTAMP"
			}
		case reflect.Array, reflect.Slice:
			if isUUID(dataValue) {
				sqlType = fmt.Sprintf("%s(36)", strDataType)
			} else if isJSON(dataValue) {
				// Adding a constraint to see ensure that the value is a well formed JSON
				sqlType = fmt.Sprintf("CLOB CHECK (%s IS JSON)", strings.ToLower(field.DBName))
			} else if IsByteArrayOrSlice(dataValue) {
				sqlType = "BLOB"
			}
		}

	} else if isUUID(dataValue) {
		// In case the user has specified uuid as the type explicitly
		sqlType = fmt.Sprintf("%s(36)", strDataType)
	}

	if len(sqlType) == 0 {
		panic(fmt.Sprintf("invalid sql type %s (%s) for oci8", dataValue.Type().Name(), dataValue.Kind().String()))
	}

	return sqlType, additionalType
}

func (o *oci8) HasIndex(tableName string, indexName string) bool {
	var count int
	o.db.QueryRow("SELECT COUNT(*) FROM USER_INDEXES WHERE TABLE_NAME = :1 AND INDEX_NAME = :2", strings.ToUpper(tableName), strings.ToUpper(indexName)).Scan(&count)
	return count > 0
}

func (o *oci8) HasForeignKey(tableName string, foreignKeyName string) bool {
	var count int
	o.db.QueryRow("SELECT COUNT(*) FROM USER_CONSTRAINTS WHERE CONSTRAINT_TYPE = 'R' AND TABLE_NAME = :1 AND CONSTRAINT_NAME = :2", strings.ToUpper(tableName), strings.ToUpper(foreignKeyName)).Scan(&count)
	return count > 0
}

func (o *oci8) HasTable(tableName string) bool {
	var count int
	o.db.QueryRow("SELECT COUNT(*) FROM USER_TABLES WHERE TABLE_NAME = :1", strings.ToUpper(tableName)).Scan(&count)
	return count > 0
}

func (o *oci8) HasColumn(tableName string, columnName string) bool {
	var count int
	o.db.QueryRow("SELECT COUNT(*) FROM USER_TAB_COLUMNS WHERE TABLE_NAME = :1 AND COLUMN_NAME = :2", strings.ToUpper(tableName), strings.ToUpper(columnName)).Scan(&count)
	return count > 0
}

func (o *oci8) ModifyColumn(tableName string, columnName string, typ string) error {
	_, err := o.db.Exec(fmt.Sprintf("ALTER TABLE %v MODIFY %v %v", tableName, columnName, typ))
	return err
}

func (s *oci8) DropNullable(tableName string, columnName string, colType string) error {
	_, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %v MODIFY %v %v NULL", tableName, columnName, colType))
	return err
}

func (*oci8) buildSha(str string) string {
	if utf8.RuneCountInString(str) <= 30 {
		return str
	}

	h := sha1.New()
	h.Write([]byte(str))
	bs := h.Sum(nil)

	result := fmt.Sprintf("%x", bs)
	if len(result) <= 30 {
		return result
	}
	return result[:29]
}

// Returns the primary key via the row ID
// Assumes that the primary key is the ID of the table
func (o *oci8) ResolveRowID(tableName string, rowID uint) uint {
	strRowID := ociDriver.GetLastInsertId(int64(rowID))
	var id float64
	query := fmt.Sprintf(`SELECT id FROM %s WHERE rowid = :2`, o.Quote(tableName))
	var err error
	if err = o.db.QueryRow(query, strRowID).Scan(&id); err == nil {
		return uint(id)
	} else {
		defaultLogger.Print(fmt.Sprintf("[warning][oci8] Unable to fetch ID for rowID %v: %v\n", strRowID, err))
	}

	return rowID
}

// Client statement separator used to terminate the statement
func (*oci8) ClientStatementSeparator() string {
	// In case of most DB's, it's a semicolon
	return ""
}

func (*oci8) LimitAndOffsetSQL(limit, offset interface{}) (sql string) {
	// In case both limit and offset are nil, simply return and empty string
	if offset == nil && limit == nil {
		return ""
	}

	var parsedLimit, parsedOffset int64
	var errLimitParse, errOffsetParse error
	// Parsing the limit and the offset beforehand
	if limit != nil {
		parsedLimit, errLimitParse = strconv.ParseInt(fmt.Sprint(limit), 0, 0);
	}
	if offset != nil {
		parsedOffset, errOffsetParse = strconv.ParseInt(fmt.Sprint(offset), 0, 0);
	}

	// Offset clause comes first
	if errOffsetParse == nil && parsedOffset >= 0 {
		sql += fmt.Sprintf(" OFFSET %d", parsedOffset)
	} else if parsedLimit > 0 {
		// Set the offset as zero in case there is no offset > 0 specified for a limit > 0
		sql += fmt.Sprintf(" OFFSET %d", 0)
	}

	// Limit clause comes later
	if errLimitParse == nil && parsedLimit >= 0 {
		sql += fmt.Sprintf(" ROWS FETCH NEXT %d ROWS ONLY", parsedLimit)
	}
	return
}

func (*oci8) ColumnEquality(fieldDBName, columnName string) bool {
	return strings.EqualFold(fieldDBName, columnName)
}

// Check if there is a dialect specific setting first, if yes, that takes precedence
// Dialect specific keys can be specified by prefixing the dialect name to the key.
// eg. A dialect specific size for postgres can be specified as "postgres size"
// Then check if there is a generic setting
func (o *oci8) GetTagSetting(field *StructField, key string) (val string, ok bool) {
	return field.TagSettingsGetFirst(strings.ToUpper(o.GetName())+" "+key, key)
}

func (o *oci8) GetByteLimit() int {
	return 30000
}
