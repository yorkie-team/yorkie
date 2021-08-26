package converter

import "errors"

var (
	// ErrPackRequired is returned when an empty pack is passed.
	ErrPackRequired = errors.New("pack required")

	// ErrCheckpointRequired is returned when a pack with an empty checkpoint is
	// passed.
	ErrCheckpointRequired = errors.New("checkpoint required")

	// ErrUnsupportedOperation is returned when the given operation is not
	// supported yet.
	ErrUnsupportedOperation = errors.New("unsupported operation")

	// ErrUnsupportedElement is returned when the given element is not
	// supported yet.
	ErrUnsupportedElement = errors.New("unsupported element")

	// ErrUnsupportedEventType is returned when the given event type is not
	// supported yet.
	ErrUnsupportedEventType = errors.New("unsupported event type")

	// ErrUnsupportedValueType is returned when the given value type is not
	// supported yet.
	ErrUnsupportedValueType = errors.New("unsupported value type")

	// ErrUnsupportedCounterType is returned when the given counter type is not
	// supported yet.
	ErrUnsupportedCounterType = errors.New("unsupported counter type")
)
