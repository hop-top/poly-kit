package rpc

import (
	"encoding/json"
	"errors"

	"google.golang.org/protobuf/types/known/structpb"
)

// StructToEntity converts a protobuf Struct to a Go entity via
// JSON intermediate.
func StructToEntity[T any](s *structpb.Struct) (T, error) {
	var zero T
	if s == nil {
		return zero, errors.New("entity payload is nil")
	}
	b, err := s.MarshalJSON()
	if err != nil {
		return zero, err
	}
	var entity T
	if err := json.Unmarshal(b, &entity); err != nil {
		return zero, err
	}
	return entity, nil
}

// EntityToStruct converts a Go entity to a protobuf Struct via
// JSON intermediate.
func EntityToStruct[T any](entity T) (*structpb.Struct, error) {
	b, err := json.Marshal(entity)
	if err != nil {
		return nil, err
	}
	s := &structpb.Struct{}
	if err := s.UnmarshalJSON(b); err != nil {
		return nil, err
	}
	return s, nil
}
