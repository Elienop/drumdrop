// Package library decides which files in the downloads and library folders
// belong to which lesson, and removes them without ever acting outside those
// folders. The worker (the library move, and the cleanup of an abandoned
// download) and the server (a lesson or follow delete) share it, so both ask
// the same questions the same way.
package library
