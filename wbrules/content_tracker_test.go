package wbrules

import (
	"github.com/contactless/wbgo"
	"testing"
)

type ContentTrackerSuite struct {
	wbgo.Suite
	*wbgo.DataFileFixture
	tracker *ContentTracker
}

func (s *ContentTrackerSuite) T() *testing.T {
	return s.Suite.T()
}

func (s *ContentTrackerSuite) SetupTest() {
	s.Suite.SetupTest()
	s.DataFileFixture = wbgo.NewDataFileFixture(s.Suite.T())
	s.tracker = NewContentTracker()
}

func (s *ContentTrackerSuite) TearDownTest() {
	s.TearDownDataFiles()
	s.Suite.TearDownTest()
}

func (s *ContentTrackerSuite) track(filename string) bool {
	r, err := s.tracker.Track(filename, s.DataFilePath(filename))
	s.Ck("Track()", err)
	return r
}

func (s *ContentTrackerSuite) TestTracking() {
	s.WriteDataFile("abc.js", "// abc.js")
	s.WriteDataFile("def.js", "// def.js")
	s.WriteDataFile("foo/bar.js", "// foo/bar.js")

	s.True(s.track("abc.js"))
	s.True(s.track("def.js"))
	s.True(s.track("foo/bar.js"))
	for i := 0; i < 3; i++ {
		s.False(s.track("abc.js"))
		s.False(s.track("def.js"))
		s.False(s.track("foo/bar.js"))
	}

	s.WriteDataFile("def.js", "// def.js (changed)")
	s.WriteDataFile("foo/bar.js", "// foo/bar.js (changed)")

	s.False(s.track("abc.js"))
	s.True(s.track("def.js"))
	s.True(s.track("foo/bar.js"))
	for i := 0; i < 3; i++ {
		s.False(s.track("abc.js"))
		s.False(s.track("def.js"))
		s.False(s.track("foo/bar.js"))
	}
}

func TestContentTrackerSuite(t *testing.T) {
	wbgo.RunSuites(t, new(ContentTrackerSuite))
}
