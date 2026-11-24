// Copyright 2024 Alola Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package netpoll

// FDOperator is re-exported by package anet, so every exported method or field on it is public API.
// The connection code still needs the operator's internal state, so it gets it through these functions:
// functions of an internal package cannot be called from outside the module.

// Do marks op as in use by the caller. It reports false if the poller or another caller holds it.
func Do(op *FDOperator) bool { return op.do() }

// Done releases op after Do.
func Done(op *FDOperator) { op.done() }

// IsUnused reports whether op is not registered with a poller.
func IsUnused(op *FDOperator) bool { return op.isUnused() }

// IsDetached reports whether op was detached from its poller.
func IsDetached(op *FDOperator) bool { return op.isDetached() }

// SetDetached marks op as detached (or not), so that Control(PollDetach) becomes a no-op.
// It exists for tests that simulate a detach race.
func SetDetached(op *FDOperator, detached bool) { op.setDetached(detached) }

// PollOf returns the poller op is registered with, or nil.
func PollOf(op *FDOperator) Poll { return op.poll }

// Bind sets the poller op belongs to, before it is registered.
func Bind(op *FDOperator, p Poll) { op.poll = p }
