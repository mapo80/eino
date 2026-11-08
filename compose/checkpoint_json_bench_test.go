/*
 * Copyright 2026 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package compose

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/internal/serialization"
	"github.com/cloudwego/eino/schema"
)

// conversationCheckpoint is a checkpoint holding a conversation of n turns
// with tool calls, as an agent node keeps it.
func conversationCheckpoint(n int) *checkpoint {
	var msgs []*schema.Message
	for i := 0; i < n; i++ {
		msgs = append(msgs,
			schema.UserMessage(fmt.Sprintf("Turno %d: vorrei una camera doppia dal 20 al 27 dicembre, vista mare <se possibile> & colazione inclusa.", i)),
			&schema.Message{Role: schema.Assistant, Content: strings.Repeat("Verifico la disponibilità. ", 4), ToolCalls: []schema.ToolCall{{
				ID: fmt.Sprintf("call_%d", i), Type: "function",
				Function: schema.FunctionCall{Name: "search_rooms", Arguments: `{"checkIn":"2026-12-20","checkOut":"2026-12-27","guests":2,"view":"sea"}`},
			}}},
			&schema.Message{Role: schema.Tool, ToolCallID: fmt.Sprintf("call_%d", i), Content: `{"rooms":[{"code":"DLX","price":1280.0},{"code":"SUP","price":1040.0}]}`,
				Extra: map[string]any{"latency_ms": 120.0, "source": "pms"}},
		)
	}
	return &checkpoint{
		Inputs:     map[string]any{"agent": msgs},
		State:      &deepCopyTestState{Vars: map[string]string{"lingua": "it", "hotel": "Callistos"}, Items: []string{"a", "b"}, Count: n},
		RerunNodes: []string{"agent"},
	}
}

// BenchmarkCheckpointJSON writes a checkpoint of a 20-turn conversation to
// bytes and reads it back with the checkpoint serializer.
func BenchmarkCheckpointJSON(b *testing.B) {
	s := &serialization.InternalSerializer{}
	cp := conversationCheckpoint(20)
	data, err := s.Marshal(cp)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.Run("marshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.Marshal(cp); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var out checkpoint
			if err := s.Unmarshal(data, &out); err != nil {
				b.Fatal(err)
			}
		}
	})
}
