package spectator

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func makeConfig(uri string) *Config {
	return &Config{10 * time.Millisecond, 1 * time.Second, uri, 10000,
		map[string]string{
			"nf.app":     "test",
			"nf.cluster": "test-main",
			"nf.asg":     "test-main-v001",
			"nf.region":  "us-west-1",
		},
		nil,
		nil,
	}
}

var config = makeConfig("")

func TestNewRegistryConfiguredBy(t *testing.T) {
	r, err := NewRegistryConfiguredBy("test_config.json")
	if err != nil {
		t.Fatal("Unable to get a registry", err)
	}

	expectedConfig := Config{
		5 * time.Second,
		1 * time.Second,
		"http://example.org/api/v4/update",
		10000,
		map[string]string{"nf.app": "app", "nf.account": "1234"},
		defaultLogger(),
		nil,
	}
	cfg := r.config
	cfg.IsEnabled = nil
	if !reflect.DeepEqual(&expectedConfig, cfg) {
		t.Errorf("Expected config %v, got %v", expectedConfig, cfg)
	}
}

func TestRegistry_Counter(t *testing.T) {
	r := NewRegistry(config)
	r.Counter("foo", nil).Increment()
	if v := r.Counter("foo", nil).Count(); v != 1 {
		t.Error("Counter needs to return a previously registered counter. Expected 1, got", v)
	}
}

func TestRegistry_DistributionSummary(t *testing.T) {
	r := NewRegistry(config)
	r.DistributionSummary("ds", nil).Record(100)
	if v := r.DistributionSummary("ds", nil).Count(); v != 1 {
		t.Error("DistributionSummary needs to return a previously registered meter. Expected 1, got", v)
	}
	if v := r.DistributionSummary("ds", nil).TotalAmount(); v != 100 {
		t.Error("Expected 100, Got", v)
	}
}

func TestRegistry_Gauge(t *testing.T) {
	r := NewRegistry(config)
	r.Gauge("g", nil).Set(100)
	if v := r.Gauge("g", nil).Get(); v != 100 {
		t.Error("Gauge needs to return a previously registered meter. Expected 100, got", v)
	}
}

func TestRegistry_Timer(t *testing.T) {
	r := NewRegistry(config)
	r.Timer("t", nil).Record(100)
	if v := r.Timer("t", nil).Count(); v != 1 {
		t.Error("Timer needs to return a previously registered meter. Expected 1, got", v)
	}
	if v := r.Timer("t", nil).TotalTime(); v != 100 {
		t.Error("Expected 100, Got", v)
	}
}

func TestRegistry_Start(t *testing.T) {
	r := NewRegistry(config)
	clock := &ManualClock{1}
	r.clock = clock
	r.Counter("foo", nil).Increment()
	r.Start()
	time.Sleep(30 * time.Millisecond)
	r.Stop()
}

type payloadEntry struct {
	tags  map[string]string
	op    int
	value float64
}

func getEntry(strings []string, payload []interface{}) (numConsumed int, entry payloadEntry) {
	numTags := int(payload[0].(float64))
	tags := make(map[string]string, numTags)
	for i := 1; i < numTags*2; i += 2 {
		keyIdx := int(payload[i].(float64))
		valIdx := int(payload[i+1].(float64))
		tags[strings[keyIdx]] = strings[valIdx]
	}
	entry.tags = tags
	entry.op = int(payload[numTags*2+1].(float64))
	entry.value = payload[numTags*2+2].(float64)
	numConsumed = numTags*2 + 3
	return
}

func payloadToEntries(t *testing.T, payload []interface{}) []payloadEntry {
	numStrings := int(payload[0].(float64))
	var strings = make([]string, numStrings)
	for i := 1; i <= numStrings; i++ {
		strings[i-1] = payload[i].(string)
	}

	var entries []payloadEntry
	curIdx := numStrings + 1
	for curIdx < len(payload) {
		numConsumed, entry := getEntry(strings[:], payload[curIdx:])
		if numConsumed == 0 {
			t.Fatalf("Could not decode payload. Last index: %d - remaining %v", curIdx, payload[curIdx:])
		}
		entries = append(entries, entry)
		curIdx += numConsumed
	}
	return entries
}

func TestRegistry_publish(t *testing.T) {
	const StartTime = 1
	clock := &ManualClock{StartTime}
	publishHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Unexpected content-type: %s", contentType)
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatal("Unable to read body", err)
		}
		var payload []interface{}
		err = json.Unmarshal(body, &payload)
		if err != nil {
			t.Fatal("Unable to unmarshal payload", err)
		}
		expected := []interface{}{
			// string table
			12.0, "count", "name", "foo", "nf.app", "nf.asg", "nf.cluster", "nf.region", "statistic", "test", "test-main", "test-main-v001", "us-west-1",
			// one measurement: a counter with value 10
			6.0, // 4 common tags, name, statistic
			//
			3.0, 8.0, 5.0, 9.0, 4.0, 10.0, 6.0, 11.0, 7.0, 0.0, 1.0, 2.0,
			// op is 0 = add
			0.0,
			// delta is 10
			10.0}

		expectedEntries := payloadToEntries(t, expected)
		payloadEntries := payloadToEntries(t, payload)

		if !reflect.DeepEqual(expectedEntries, payloadEntries) {
			t.Errorf("Expected payload:\n %v\ngot:\n %v", expectedEntries, payloadEntries)
		}

		w.Write(okMsg)

		clock.SetNanos(StartTime + 1000)
	})

	server := httptest.NewServer(publishHandler)
	defer server.Close()

	serverUrl := server.URL

	cfg := makeConfig(serverUrl)
	r := NewRegistry(cfg)
	r.clock = clock

	r.Counter("foo", nil).Add(10)
	r.publish()
}

func assertEqual(t *testing.T, a interface{}, b interface{}, message string) {
	if a != b {
		msg := fmt.Sprintf("%v != %v", a, b)
		if len(message) > 0 {
			msg = fmt.Sprintf("%s (%s)", message, msg)
		}
		t.Fatal(msg)
	}
}

func TestRegistry_enabled(t *testing.T) {
	called := 0
	publishHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
	})

	server := httptest.NewServer(publishHandler)
	defer server.Close()

	serverUrl := server.URL

	cfg := makeConfig(serverUrl)
	enabled := true
	cfg.IsEnabled = func() bool {
		return enabled
	}
	r := NewRegistry(cfg)

	r.Counter("foo", nil).Add(10)
	r.publish()

	assertEqual(t, called, 1, "expected 1 publish call")

	r.Counter("foo", nil).Add(10)
	r.publish()

	assertEqual(t, called, 2, "expected 2 publish calls")

	enabled = false
	r.Counter("foo", nil).Add(10)
	r.publish()
	assertEqual(t, called, 2, "expected no extra publish calls")

	enabled = true
	r.Counter("foo", nil).Add(10)
	r.publish()
	assertEqual(t, called, 3, "expected 3 publish calls")
}

func withDefaultTags(tags ...Tag) []Tag {
	defaultTags := []Tag{}
	for k, v := range config.CommonTags {
		defaultTags = append(defaultTags, Tag{Key: k, Value: v})
	}
	for _, t := range tags {
		defaultTags = append(defaultTags, t)
	}
	return defaultTags
}

func TestConvert(t *testing.T) {

	cases := map[string]struct {
		setup           func(*Registry)
		expectedOutputs map[string]Metric
	}{
		"one counter": {
			setup: func(r *Registry) {
				cntr := r.Counter("test.test", map[string]string{})
				cntr.Increment()
			},
			expectedOutputs: map[string]Metric{
				"test.test": Metric{
					Kind: "Counter",
					Values: []TopValue{
						TopValue{
							Tags: withDefaultTags(Tag{Key: "statistic", Value: "count"}),
							Values: []*Value{
								&Value{V: 1, T: 0},
							},
						},
					},
				},
			},
		},
		"one counter with extra tags": {
			setup: func(r *Registry) {
				cntr := r.Counter("test.test", map[string]string{
					"test":  "yes",
					"yoda":  "false",
					"rodeo": "true",
				})
				cntr.Increment()
			},
			expectedOutputs: map[string]Metric{
				"test.test": Metric{
					Kind: "Counter",
					Values: []TopValue{
						TopValue{
							Tags: withDefaultTags(
								Tag{Key: "statistic", Value: "count"},
								Tag{Key: "test", Value: "yes"},
								Tag{Key: "yoda", Value: "false"},
								Tag{Key: "rodeo", Value: "true"},
							),
							Values: []*Value{
								&Value{V: 1, T: 0},
							},
						},
					},
				},
			},
		},
		"two counters": {
			setup: func(r *Registry) {
				cntr1 := r.Counter("test.one", map[string]string{})
				cntr1.Increment()
				cntr2 := r.Counter("test.two", map[string]string{})
				cntr2.Increment()
				cntr2.Increment()
			},
			expectedOutputs: map[string]Metric{
				"test.one": Metric{
					Kind: "Counter",
					Values: []TopValue{
						TopValue{
							Tags: withDefaultTags(Tag{Key: "statistic", Value: "count"}),
							Values: []*Value{
								&Value{V: 1, T: 0},
							},
						},
					},
				},
				"test.two": Metric{
					Kind: "Counter",
					Values: []TopValue{
						TopValue{
							Tags: withDefaultTags(Tag{Key: "statistic", Value: "count"}),
							Values: []*Value{
								&Value{V: 2, T: 0},
							},
						},
					},
				},
			},
		},
		"one timer": {
			setup: func(r *Registry) {
				tmer := r.Timer("test.test", map[string]string{})
				tmer.Record(1 * time.Second)
			},
			expectedOutputs: map[string]Metric{
				"test.test": Metric{
					Kind: "Timer",
					Values: []TopValue{
						TopValue{
							Tags: withDefaultTags(Tag{Key: "statistic", Value: "count"}),
							Values: []*Value{
								&Value{V: 1, T: 0},
							},
						},
						TopValue{
							Tags: withDefaultTags(Tag{Key: "statistic", Value: "totalTime"}),
							Values: []*Value{
								&Value{V: 100000, T: 0},
							},
						},
						TopValue{
							Tags: withDefaultTags(Tag{Key: "statistic", Value: "totalOfSquares"}),
							Values: []*Value{
								&Value{V: 100000, T: 0},
							},
						},
						TopValue{
							Tags: withDefaultTags(Tag{Key: "statistic", Value: "max"}),
							Values: []*Value{
								&Value{V: 100000, T: 0},
							},
						},
					},
				},
			},
		},
	}

	for testName, c := range cases {
		t.Run(testName, func(t *testing.T) {
			r := NewRegistry(config)
			c.setup(r)
			out := Convert(r)

			// verify same length
			assert.Equal(t, len(out), len(c.expectedOutputs), "Results should have the same length")
			// drill down and compare
			for name, metric := range c.expectedOutputs {
				assert.Contains(t, out, name, "Metric should exist in output")
				outmetric := out[name]
				kind := metric.Kind
				assert.Equal(t, kind, outmetric.Kind, "Metric kind should be equal")
				assert.Equal(t, len(metric.Values), len(outmetric.Values), "Metric values should have the same length")

				for i, topvalue := range metric.Values {

					outtopvalue := outmetric.Values[i]
					statistic := ""
					for _, tag := range topvalue.Tags {
						if tag.Key == "statistic" {
							statistic = tag.Value
						}
					}

					// tags should match
					assert.ElementsMatch(t, topvalue.Tags, outtopvalue.Tags, "Tags should match")
					assert.NotEqual(t, "", statistic, "Tag Statistic should exist")

					// values should meet expectations based on metric kind
					switch kind {
					case "Timer":
						if statistic == "count" {
							assert.Equal(t, topvalue.Values[0].V, outtopvalue.Values[0].V, "Timer values of statistic count should match")
						} else {
							// Timer values for statistics max, totalTime, and totalOfSquares cannot be exact
							assert.GreaterOrEqualf(t, outtopvalue.Values[0].V, topvalue.Values[0].V, "Timer values of statistic %s should be >= expected values", statistic)
						}
					default:
						assert.Equalf(t, topvalue.Values[0].V, outtopvalue.Values[0].V, "Values for metric kind %s and statistic %s should match", kind, statistic)
					}
				}
			}
		})
	}
}
