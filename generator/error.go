package generator

func newError(text string) error {
	return &idGenError{text}
}

// errorString is a trivial implementation of error.
type idGenError struct {
	s string
}

func (e *idGenError) Error() string {
	return e.s
}
