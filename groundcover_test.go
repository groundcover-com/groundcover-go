// Copyright 2026 groundcover Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package groundcover

import "testing"

func TestSDKVersion(t *testing.T) {
	if SDKVersion == "" {
		t.Error("SDKVersion must not be empty")
	}
}

func TestSDKName(t *testing.T) {
	if SDKName == "" {
		t.Error("SDKName must not be empty")
	}

	expected := "groundcover.go"
	if SDKName != expected {
		t.Errorf("SDKName = %q, want %q", SDKName, expected)
	}
}
