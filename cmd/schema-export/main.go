package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/m-lab/go/flagx"

	"cloud.google.com/go/bigquery"
	"github.com/m-lab/go/bqx"
	"github.com/m-lab/go/rtx"
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

var special = map[string]bigquery.FieldSchema{
	"IDiagSPort":        bigquery.FieldSchema{Name: "IDiagSPort", Description: "", Repeated: false, Required: false, Type: "INTEGER"},
	"IDiagDPort":        bigquery.FieldSchema{Name: "IDiagDPort", Description: "", Repeated: false, Required: false, Type: "INTEGER"},
	"IDiagSrc":          bigquery.FieldSchema{Name: "IDiagSrc", Description: "", Repeated: false, Required: false, Type: "STRING"},
	"IDiagDst":          bigquery.FieldSchema{Name: "IDiagDst", Description: "", Repeated: false, Required: false, Type: "STRING"},
	"IDiagIf":           bigquery.FieldSchema{Name: "IDiagIf", Description: "", Repeated: false, Required: false, Type: "INTEGER"},
	"IDiagCookie":       bigquery.FieldSchema{Name: "IDiagCookie", Description: "", Repeated: false, Required: false, Type: "INTEGER"},
	"Snapshot.Metadata": bigquery.FieldSchema{},
}

var (
	ErrInvalidProject = errors.New("Invalid project name")
	ErrInvalidDataset = errors.New("Invalid dataset name")
	ErrInvalidTable   = errors.New("Invalid table name")
	ErrInvalidFQTable = errors.New("Invalid fully qualified table name")

	fqTable = flag.String("table", "", "BQ table to create or update table=project.dataset.table")

	// TODO - move this to go repo?
	projectRegex = regexp.MustCompile("[a-z0-9-]+")
	datasetRegex = regexp.MustCompile("[a-zA-Z0-9_]+")
	tableRegex   = regexp.MustCompile("[a-zA-Z0-9_]+")
)

type pdt struct {
	project string
	dataset string
	table   string
}

func parsePDT(fq string) (*pdt, error) {
	parts := strings.Split(fq, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidFQTable
	}
	if !projectRegex.MatchString(parts[0]) {
		return nil, ErrInvalidProject
	}
	if !datasetRegex.MatchString(parts[1]) {
		return nil, ErrInvalidDataset
	}
	if !tableRegex.MatchString(parts[2]) {
		return nil, ErrInvalidTable
	}
	return &pdt{parts[0], parts[1], parts[2]}, nil
}

// TODO move to bqext
func createOrUpdateTable(ctx context.Context, table pdt,
	schema bigquery.Schema, partitioning bigquery.TimePartitioning, clustering bigquery.Clustering) error {
	client, err := bigquery.NewClient(ctx, table.project)
	rtx.Must(err, "")

	ds := client.Dataset(table.dataset)

	ds.Create(ctx, nil)

	t := ds.Table(table.table)

	meta, err := t.Metadata(ctx)
	if err != nil {
		// Table probably doesn't exist
		log.Println(err)

		meta = &bigquery.TableMetadata{Schema: schema}
		meta.TimePartitioning = partitioning
		meta.Clustering = clustering

		err = t.Create(ctx, meta)
		if err != nil {
			log.Println(err)
		}
		return err
	}

	changes := bigquery.TableMetadataToUpdate{
		Schema: schema,
	}

	md, err := t.Update(ctx, changes, meta.ETag)
	if err != nil {
		log.Println(err)
		return err
	}
	log.Printf("%+v\n", md)
	return nil
}

type bqentry struct {
	UUID      string    // Top level just because
	TestTime  time.Time // Must be top level for partitioning
	Snapshots []*snapshot.Snapshot
}

func main() {
	flag.Parse()
	rtx.Must(flagx.ArgsFromEnv(flag.CommandLine), "Could not parse flags from the environment")
	if len(flag.Args()) > 0 {
		log.Fatal("Unrecognized arguments")
	}

	schema, err := bigquery.InferSchema(bqentry{})
	if err != nil {
		log.Fatal(err)
	}

	c := bqx.Customize(schema, special)
	rr := bqx.RemoveRequired(c)

	pp, _ := bqx.PrettyPrint(rr, true)
	fmt.Print(pp)

	if *fqTable != "" {
		table, err := parsePDT(*fqTable)
		rtx.Must(err, "")
		err = createOrUpdateTable(context.Background(), *table, rr,
			&bigquery.TimePartitioning{Field: "TestTime"}, nil)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}
	}
}
