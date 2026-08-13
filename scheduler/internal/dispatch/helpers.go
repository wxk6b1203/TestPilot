package dispatch

import (
	"encoding/json"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

func structToJSON(s *structpb.Struct) model.JSON {
	if s == nil {
		return model.JSON("null")
	}
	b, err := protojson.Marshal(s)
	if err != nil {
		return model.JSON("null")
	}
	return model.JSON(b)
}

func assertionsToJSON(list []*commonv1.AssertionResult) model.JSON {
	arr := make([]json.RawMessage, 0, len(list))
	for _, a := range list {
		if b, err := protojson.Marshal(a); err == nil {
			arr = append(arr, b)
		}
	}
	if len(arr) == 0 {
		return model.JSON("[]")
	}
	b, err := json.Marshal(arr)
	if err != nil {
		return model.JSON("[]")
	}
	return model.JSON(b)
}

func stringsToJSON(list []string) model.JSON {
	if len(list) == 0 {
		return model.JSON("[]")
	}
	b, err := json.Marshal(list)
	if err != nil {
		return model.JSON("[]")
	}
	return model.JSON(b)
}
