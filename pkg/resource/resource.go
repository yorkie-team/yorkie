/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package resource

// DocSize represents the size of a document in bytes.
type DocSize struct {
	Live DataSize
	GC   DataSize
}

// Total returns the total size of the document in bytes.
func (d *DocSize) Total() int {
	return d.Live.Total() + d.GC.Total()
}

// DataSize represents the size of a resource in bytes.
type DataSize struct {
	Data int
	Meta int
}

// Total returns the total size of the resource in bytes.
func (d *DataSize) Total() int {
	return d.Data + d.Meta
}

// Add adds the sizes of other resources.
func (d *DataSize) Add(others ...DataSize) {
	for _, diff := range others {
		d.Data += diff.Data
		d.Meta += diff.Meta
	}
}

// Sub subtracts the size of other resource from this one.
func (d *DataSize) Sub(other DataSize) {
	d.Data -= other.Data
	d.Meta -= other.Meta
}
