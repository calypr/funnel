package mongodb

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/calypr/funnel/tes"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Older Funnel releases stored output sizes as BSON integers; current TES uses strings.
var taskRegistry = func() *bson.Registry {
	r := bson.NewRegistry()
	r.RegisterTypeDecoder(reflect.TypeOf(tes.OutputFileLog{}), bson.ValueDecoderFunc(decodeOutputFileLog))
	return r
}()

func decodeOutputFileLog(dc bson.DecodeContext, reader bson.ValueReader, value reflect.Value) error {
	var document struct {
		Path      string
		URL       string        `bson:"url"`
		SizeBytes bson.RawValue `bson:"sizebytes"`
	}
	decoder, err := dc.LookupDecoder(reflect.TypeOf(document))
	if err != nil {
		return err
	}
	if err := decoder.DecodeValue(dc, reader, reflect.ValueOf(&document).Elem()); err != nil {
		return err
	}
	var size string
	switch document.SizeBytes.Type {
	case 0, bson.TypeNull, bson.TypeUndefined:
	case bson.TypeString:
		size = document.SizeBytes.StringValue()
	case bson.TypeInt32:
		size = strconv.FormatInt(int64(document.SizeBytes.Int32()), 10)
	case bson.TypeInt64:
		size = strconv.FormatInt(document.SizeBytes.Int64(), 10)
	default:
		return fmt.Errorf("invalid output size BSON type %s", document.SizeBytes.Type)
	}
	out := value.Addr().Interface().(*tes.OutputFileLog)
	out.Path, out.Url, out.SizeBytes = document.Path, document.URL, size
	return nil
}
