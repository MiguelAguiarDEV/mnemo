// Package cloudstore implements the Postgres-backed storage layer for Engram Cloud.
//
// It mirrors the local SQLite store but uses Postgres with row-level user
// isolation, full-text search via tsvector, and chunk-based sync storage.
package cloudstore
