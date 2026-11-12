package profile

import (
	"errors"
	"testing"
)

func TestSource_Validate(t *testing.T) {
	for _, s := range []Source{SourceOAuth, SourceAPIKey} {
		if err := s.Validate(); err != nil {
			t.Errorf("%q.Validate() = %v, want nil", s, err)
		}
	}
	for _, s := range []Source{"", "enterprise", "OAUTH"} {
		err := s.Validate()
		if !errors.Is(err, ErrUnknownSource) {
			t.Errorf("%q.Validate() = %v, want ErrUnknownSource", s, err)
		}
	}
}
