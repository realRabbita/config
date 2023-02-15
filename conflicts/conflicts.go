//go:build static && system_libgit2

package main

import (
	"context"
	"encoding/gob"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	git "github.com/libgit2/git2go/v33"
	"gitlab.com/gitlab-org/gitaly/v15/cmd/gitaly-git2go-v15/git2goutil"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type conflictsSubcommand struct{}

func (cmd *conflictsSubcommand) Flags() *flag.FlagSet {
	return flag.NewFlagSet("conflicts", flag.ExitOnError)
}

func (cmd *conflictsSubcommand) Run(_ context.Context, decoder *gob.Decoder, encoder *gob.Encoder) error {
	var request git2go.ConflictsCommand
	if err := decoder.Decode(&request); err != nil {
		return err
	}
	res := cmd.conflicts(request)
	return encoder.Encode(res)
}

func (conflictsSubcommand) conflicts(request git2go.ConflictsCommand) git2go.ConflictsResult {
	f, _ := os.Create("runtime.log")
	defer f.Close()

	t0 := time.Now()
	repo, err := git2goutil.OpenRepository(request.Repository)
	if err != nil {
		return conflictError(codes.Internal, fmt.Errorf("could not open repository: %w", err).Error())
	}
	fmt.Fprintln(f, "open repository time:", time.Since(t0))
	t1 := time.Now()

	oursOid, err := git.NewOid(request.Ours)
	if err != nil {
		return conflictError(codes.InvalidArgument, err.Error())
	}

	ours, err := repo.LookupCommit(oursOid)
	if err != nil {
		return convertError(err, git.ErrorCodeNotFound, codes.InvalidArgument)
	}

	theirsOid, err := git.NewOid(request.Theirs)
	if err != nil {
		return conflictError(codes.InvalidArgument, err.Error())
	}

	theirs, err := repo.LookupCommit(theirsOid)
	if err != nil {
		return convertError(err, git.ErrorCodeNotFound, codes.InvalidArgument)
	}
	fmt.Fprintln(f, "lookup commit time:", time.Since(t1))
	t2 := time.Now()

	index, err := repo.MergeCommits(ours, theirs, nil)
	if err != nil {
		return conflictError(codes.FailedPrecondition, fmt.Sprintf("could not merge commits: %v", err))
	}

	fmt.Fprintln(f, "merge commits time:", time.Since(t2))
	t3 := time.Now()

	iterator, err := index.ConflictIterator()
	if err != nil {
		return conflictError(codes.Internal, fmt.Errorf("could not get conflicts: %w", err).Error())
	}

	var result git2go.ConflictsResult
	for {
		conflict, err := iterator.Next()
		if err != nil {
			var gitError *git.GitError
			if errors.As(err, &gitError) && gitError.Code == git.ErrorCodeIterOver {
				break
			}
			return conflictError(codes.Internal, err.Error())
		}

		merge, err := Merge(repo, conflict)
		if err != nil {
			if s, ok := status.FromError(err); ok {
				return conflictError(s.Code(), s.Message())
			}
			return conflictError(codes.Internal, err.Error())
		}

		result.Conflicts = append(result.Conflicts, git2go.Conflict{
			Ancestor: conflictEntryFromIndex(conflict.Ancestor),
			Our:      conflictEntryFromIndex(conflict.Our),
			Their:    conflictEntryFromIndex(conflict.Their),
			Content:  merge.Contents,
		})
	}

	fmt.Fprintln(f, "merge conflicts time:", time.Since(t3))
	fmt.Fprintln(f, "conflicts run time:", time.Since(t0))

	return result
}

// Merge will merge the given index conflict and produce a file with conflict
// markers.
func Merge(repo *git.Repository, conflict git.IndexConflict) (*git.MergeFileResult, error) {
	var ancestor, our, their git.MergeFileInput

	for entry, input := range map[*git.IndexEntry]*git.MergeFileInput{
		conflict.Ancestor: &ancestor,
		conflict.Our:      &our,
		conflict.Their:    &their,
	} {
		if entry == nil {
			continue
		}

		blob, err := repo.LookupBlob(entry.Id)
		if err != nil {
			return nil, helper.ErrFailedPreconditionf("could not get conflicting blob: %w", err)
		}

		input.Path = entry.Path
		input.Mode = uint(entry.Mode)
		input.Contents = blob.Contents()
	}

	merge, err := git.MergeFile(ancestor, our, their, nil)
	if err != nil {
		return nil, fmt.Errorf("could not compute conflicts: %w", err)
	}

	// In a case of tree-based conflicts (e.g. no ancestor), fallback to `Path`
	// of `their` side. If that's also blank, fallback to `Path` of `our` side.
	// This is to ensure that there's always a `Path` when we try to merge
	// conflicts.
	if merge.Path == "" {
		if their.Path != "" {
			merge.Path = their.Path
		} else {
			merge.Path = our.Path
		}
	}

	return merge, nil
}

func conflictEntryFromIndex(entry *git.IndexEntry) git2go.ConflictEntry {
	if entry == nil {
		return git2go.ConflictEntry{}
	}
	return git2go.ConflictEntry{
		Path: entry.Path,
		Mode: int32(entry.Mode),
	}
}

func conflictError(code codes.Code, message string) git2go.ConflictsResult {
	err := git2go.ConflictError{
		Code:    code,
		Message: message,
	}
	return git2go.ConflictsResult{
		Err: err,
	}
}

func convertError(err error, errorCode git.ErrorCode, returnCode codes.Code) git2go.ConflictsResult {
	var gitError *git.GitError
	if errors.As(err, &gitError) && gitError.Code == errorCode {
		return conflictError(returnCode, err.Error())
	}
	return conflictError(codes.Internal, err.Error())
}
