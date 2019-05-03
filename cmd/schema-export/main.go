package main

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"unsafe"

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
	// TODO This doesn't actually work
	if f.Tag == "" {
		f.Tag = `bigquery:,nullable`
	} else if _, ok := f.Tag.Lookup("bigquery"); !ok {
		f.Tag = f.Tag + ` bigquery:,nullable`
	}

	switch f.Type.Kind() {
	// These are all fine
	case reflect.String:
		//f.Type = reflect.TypeOf((*bigquery.NullString)(nil)).Elem()
	case reflect.Uint8:
		fallthrough
	case reflect.Int16:
		fallthrough
	case reflect.Uint16:
		fallthrough
	case reflect.Int32:
		fallthrough
	case reflect.Uint32:
		fallthrough
	case reflect.Int64:
		//f.Type = reflect.TypeOf((*bigquery.NullInt64)(nil)).Elem()
	case reflect.Uint64:
		// Convert uint64 to int64, since bigquery can't handle uint64
		f.Type = reflect.TypeOf((*int64)(nil)).Elem()
		return f
	case reflect.Bool:
		//f.Type = reflect.TypeOf((*bigquery.NullBool)(nil)).Elem()

		// Have to handle pointer.
	case reflect.Ptr:
		if f.Type.Elem().Kind() == reflect.Struct {
			log.Println(space, f.Name, f.Type.Elem().Size(), f.Type)
		}
		f.Type = reflect.PtrTo(handleType(f.Type.Elem(), space))
		return f

	case reflect.Struct:
		log.Println(space, f.Name, f.Type)
		f.Type = handleType(f.Type, space)
		return f
	case reflect.Array:
		if f.Type.Elem().Kind() == reflect.Uint64 {
			f.Type = reflect.ArrayOf(f.Type.Len(), reflect.TypeOf((*int64)(nil)).Elem())
		}
		log.Printf("%s%s [%d]%s\n", space, f.Name, f.Type.Len(), f.Type.Elem())
		return f
	case reflect.Slice:
		log.Fatal("Slice", f.Name, f.Type.Elem())
	default:
		log.Fatal("Unhandled", f.Name, f.Type.Kind())
		return f
	}
	log.Println(space, f.Name, f.Type)
	return f
}

// Sanitizes an input type, producing an output type.
// Converts all uint64 to int64.
func handleType(t reflect.Type, space string) reflect.Type {
	switch t.Kind() {
	case reflect.Ptr:
		// dereference
		return handleType(t.Elem(), space+"  ")
	case reflect.Struct:
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

// Iterate through all the fields, and pick up bqdesc fields
func addDescriptions(snap reflect.Type, schema bigquery.Schema) {

}

func main() {
	snap := snapshot.Snapshot{}
	rt := reflect.TypeOf(snap)
	log.Println(rt)
	t := handleType(rt, "")
	v := reflect.New(t)
	x := v.Interface()

	schema, err := bigquery.InferSchema(x)
	if err != nil {
		log.Fatal(err)
	}

	addDescriptions(rt, schema)

	log.Println(spew.Sdump(schema))

	jsonBytes, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		log.Fatal(err)
	}

	lines := strings.Split(string(jsonBytes), "\n")
	before := ""
	for _, line := range lines {
		// Remove Required from all fields.
		trim := strings.Trim(strings.TrimSpace(line), ",") // remove leading space, trailing comma
		switch trim {
		case `"Repeated": false`:
			// omit
		case `"Required": true`:
			// omit
		case `"Schema": null`:
			// omit
		case `"Schema": [`:
			fallthrough
		case `[`:
			fmt.Printf("%s%s\n", before, trim)
			before = ""
		case `{`:
			fmt.Print(line)
			before = ""
		case `}`:
			fmt.Println(strings.TrimSpace(line))
		case `]`:
			fmt.Print(line)
			before = ""
		default:
			fmt.Print(before, trim)
			before = ", "
		}
	}
	fmt.Println()

	// The sizes and offsets won't match, because we dropped some unexported fields.
	log.Println(unsafe.Sizeof(snapshot.Snapshot{}))
	log.Println(reflect.TypeOf(x).Elem().Size())

	log.Printf("%+v\n", snap)
	log.Printf("%+v\n", x)
	return
}
