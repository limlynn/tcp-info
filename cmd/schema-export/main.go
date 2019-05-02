package main

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"

	"cloud.google.com/go/bigquery"
	"github.com/davecgh/go-spew/spew"
	"github.com/m-lab/tcp-info/snapshot"
)

func init() {
	// Always prepend the filename and line number.
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

var (
	// A variable to enable mocking for testing.
	logFatal = log.Fatal
)

func handleField(f reflect.StructField, space string) reflect.StructField {
	var fake int64
	switch f.Type.Kind() {
	// These are all fine
	case reflect.String:
	case reflect.Uint8:
	case reflect.Int16:
	case reflect.Uint16:
	case reflect.Int32:
	case reflect.Uint32:
	case reflect.Int64:
	case reflect.Bool:

		// Have to handle pointer.
	case reflect.Ptr:
		f.Type = handleType(f.Type.Elem(), space)
		return f

	case reflect.Uint64:
		f.Type = reflect.TypeOf(fake)
		return f
	case reflect.Struct:
		f.Type = handleType(f.Type, space)
		return f
	case reflect.Array:
		if f.Type.Elem().Kind() == reflect.Uint64 {
			f.Type = reflect.ArrayOf(f.Type.Len(), reflect.TypeOf(fake))
		}
		return f
	case reflect.Slice:
		log.Println("Slice")
		return f
	default:
		log.Println("Unhandled", f.Name, f.Type.Kind())
		return f
	}
	log.Println(space, f.Name, f.Type)
	return f
}

// Sanitizes an input type, producing an output type.
// We want to end up with
func handleType(t reflect.Type, space string) reflect.Type {
	switch t.Kind() {
	case reflect.Ptr:
		// dereference
		log.Println(space, "deref", t.Elem())
		return handleType(t.Elem(), space+"  ")
	case reflect.Struct:
		log.Println(space, t)
		result := make([]reflect.StructField, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).PkgPath != "" {
				// log.Println("Skip", t.Field(i).Name)
				continue
			}
			result = append(result, handleField(t.Field(i), space+"  "))
		}
		return reflect.StructOf(result)
	default:
		log.Println(space, t)
	}
	return nil
}

func main() {
	spew.Dump(snapshot.Snapshot{})
	t := handleType(reflect.TypeOf(snapshot.Snapshot{}), "")
	spew.Dump(t)
	return

	schema, err := bigquery.InferSchema(snapshot.Snapshot{})
	if err != nil {
		log.Fatal(err)
	}

	jsonBytes, err := json.Marshal(schema)
	if err != nil {
		log.Fatal(err)
	}

	result := strings.ReplaceAll(string(jsonBytes), "uint64", "int64")
	fmt.Println(result)
}
