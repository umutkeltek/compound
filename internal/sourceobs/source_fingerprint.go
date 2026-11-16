package sourceobs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
)

// Changes to inode/device/ctime catch a replaced file even when an external
// copier preserves its length and modification timestamp. Atime is excluded:
// reading a source must not make the next refresh ingest it again.
func sourceFingerprint(info os.FileInfo) string {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.Struct {
		var fields []any
		for _, name := range []string{"Dev", "Ino", "Ctim", "Ctimespec", "Ctime", "Ctimensec"} {
			f := value.FieldByName(name)
			if f.IsValid() && f.CanInterface() {
				fields = append(fields, f.Interface())
			}
		}
		sum := sha256.Sum256([]byte(fmt.Sprint(info.Size(), info.ModTime().UnixNano(), info.Mode(), fields)))
		return hex.EncodeToString(sum[:])
	}
	return fmt.Sprintf("%d:%d:%s", info.Size(), info.ModTime().UnixNano(), info.Mode())
}
