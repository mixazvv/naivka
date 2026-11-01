//go:build !windows

package main

import "errors"

func readRepairRoutes() ([]byte, error) { return nil, errors.New("ROUTES_UNKNOWN") }
