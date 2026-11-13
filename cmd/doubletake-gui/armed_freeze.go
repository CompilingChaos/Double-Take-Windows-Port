//go:build windows || (linux && cgo && desktop)

package main

import "sync"

type freezeArm struct {
	mu     sync.Mutex
	target string
	frozen bool
}

func (a *freezeArm) Arm(target string, frozen bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.target, a.frozen = target, frozen
	return nil
}

func (a *freezeArm) ArmedState() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.target, a.frozen
}

func (a *freezeArm) Disarm(target string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if target == "" || a.target == target {
		a.target = ""
	}
}

func (a *freezeArm) take() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	target, frozen := a.target, a.frozen
	a.target = ""
	return target, frozen
}
