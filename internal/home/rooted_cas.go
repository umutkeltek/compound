package home

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// RootedFileCASState describes whether a rooted file publication is safely
// reversible. Ambiguous publications retain both complete versions for manual
// repair instead of guessing which one owns authority.
type RootedFileCASState string

const (
	RootedFileCASNotPublished RootedFileCASState = "not_published"
	RootedFileCASPublished    RootedFileCASState = "published"
	RootedFileCASAmbiguous    RootedFileCASState = "ambiguous_manual_repair"
)

// RootedFileCAS owns an atomic publication until Commit or Rollback resolves
// the displaced preimage. The supplied root remains owned by the caller.
type RootedFileCAS struct {
	root            *os.Root
	canonicalName   string
	recoveryName    string
	expectedAbsent  bool
	replacementData []byte
	replacementInfo os.FileInfo
	displacedData   []byte
	displacedInfo   os.FileInfo
	state           RootedFileCASState
	resolved        bool
}

func (publication *RootedFileCAS) State() RootedFileCASState {
	if publication == nil {
		return RootedFileCASNotPublished
	}
	return publication.state
}

// BeginRootedFileCAS atomically publishes a prepared sibling file only if the
// canonical entry still matches the caller's snapshot. Existing entries use a
// kernel name exchange so the displaced file can be validated after the
// linearization point; an absent entry uses a no-replace hard link.
func BeginRootedFileCAS(
	root *os.Root,
	canonicalName string,
	recoveryName string,
	expectedData []byte,
	expectedInfo os.FileInfo,
	replacementData []byte,
	replacementInfo os.FileInfo,
) (*RootedFileCAS, error) {
	publication := &RootedFileCAS{
		root:            root,
		canonicalName:   canonicalName,
		recoveryName:    recoveryName,
		expectedAbsent:  expectedInfo == nil,
		replacementData: append([]byte(nil), replacementData...),
		replacementInfo: replacementInfo,
		state:           RootedFileCASNotPublished,
	}
	if root == nil {
		return publication, fmt.Errorf("rooted file CAS requires a root")
	}
	if err := validateRootedCASLeaf(canonicalName); err != nil {
		return publication, fmt.Errorf("canonical rooted file CAS name: %w", err)
	}
	if err := validateRootedCASLeaf(recoveryName); err != nil {
		return publication, fmt.Errorf("recovery rooted file CAS name: %w", err)
	}
	if canonicalName == recoveryName {
		return publication, fmt.Errorf("rooted file CAS names must differ")
	}
	preparedData, preparedInfo, err := readRootedCASRegularFile(root, recoveryName)
	if err != nil {
		return publication, fmt.Errorf("read rooted file CAS replacement: %w", err)
	}
	if replacementInfo == nil || !os.SameFile(replacementInfo, preparedInfo) || !bytes.Equal(replacementData, preparedData) {
		return publication, fmt.Errorf("rooted file CAS replacement changed before publication")
	}
	publication.replacementInfo = preparedInfo

	if expectedInfo == nil {
		if err := root.Link(recoveryName, canonicalName); err != nil {
			return publication, fmt.Errorf("conditionally publish absent rooted file: %w", err)
		}
		publication.state = RootedFileCASPublished
		if err := publication.validateLinkedPublication(); err != nil {
			return publication.failAndRollback(err)
		}
		if err := syncRootedCASDirectory(root); err != nil {
			publication.state = RootedFileCASAmbiguous
			return publication, fmt.Errorf("sync conditionally published rooted file: %w", err)
		}
		return publication, nil
	}

	if err := exchangeRootedCASFiles(root, recoveryName, canonicalName); err != nil {
		return publication, fmt.Errorf("atomically exchange rooted file publication: %w", err)
	}
	publication.state = RootedFileCASPublished
	if err := publication.captureExchangedFiles(); err != nil {
		publication.state = RootedFileCASAmbiguous
		return publication, err
	}
	if !os.SameFile(expectedInfo, publication.displacedInfo) || !bytes.Equal(expectedData, publication.displacedData) {
		return publication.failAndRollback(fmt.Errorf("rooted file CAS preimage changed before publication"))
	}
	if err := syncRootedCASDirectory(root); err != nil {
		publication.state = RootedFileCASAmbiguous
		return publication, fmt.Errorf("sync atomically exchanged rooted file: %w", err)
	}
	return publication, nil
}

// Commit makes the publication final by removing the exact displaced preimage
// only while both names still identify the versions captured by Begin.
func (publication *RootedFileCAS) Commit() error {
	if publication == nil || publication.state != RootedFileCASPublished || publication.resolved {
		return fmt.Errorf("rooted file CAS publication is not committable")
	}
	if publication.expectedAbsent {
		if err := publication.validateLinkedPublication(); err != nil {
			publication.state = RootedFileCASAmbiguous
			return err
		}
	} else {
		if err := publication.validateExchangedPublication(); err != nil {
			publication.state = RootedFileCASAmbiguous
			return err
		}
	}
	if err := publication.root.Remove(publication.recoveryName); err != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("remove rooted file CAS recovery preimage: %w", err)
	}
	if err := syncRootedCASDirectory(publication.root); err != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("sync committed rooted file CAS: %w", err)
	}
	publication.resolved = true
	return nil
}

// Rollback restores the displaced entry with the same atomic primitive. It
// removes the prepared postimage only after both swapped identities are proven.
func (publication *RootedFileCAS) Rollback() error {
	if publication == nil || publication.state != RootedFileCASPublished || publication.resolved {
		return fmt.Errorf("rooted file CAS publication is not rollbackable")
	}
	if publication.expectedAbsent {
		if err := publication.validateLinkedPublication(); err != nil {
			publication.state = RootedFileCASAmbiguous
			return err
		}
		if err := publication.root.Remove(publication.canonicalName); err != nil {
			publication.state = RootedFileCASAmbiguous
			return fmt.Errorf("remove conditionally published rooted file: %w", err)
		}
		if err := syncRootedCASDirectory(publication.root); err != nil {
			publication.state = RootedFileCASAmbiguous
			return fmt.Errorf("sync rolled back absent rooted file CAS: %w", err)
		}
		if err := publication.root.Remove(publication.recoveryName); err != nil {
			publication.state = RootedFileCASAmbiguous
			return fmt.Errorf("remove rolled-back absent rooted file CAS postimage: %w", err)
		}
		if err := syncRootedCASDirectory(publication.root); err != nil {
			publication.state = RootedFileCASAmbiguous
			return fmt.Errorf("sync absent rooted file CAS rollback cleanup: %w", err)
		}
		publication.state = RootedFileCASNotPublished
		publication.resolved = true
		return nil
	}

	canonicalData, canonicalInfo, err := readRootedCASRegularFile(publication.root, publication.canonicalName)
	if err != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("read rooted file CAS postimage before rollback: %w", err)
	}
	if !os.SameFile(publication.replacementInfo, canonicalInfo) || !bytes.Equal(publication.replacementData, canonicalData) {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("refusing rooted file CAS rollback after published postimage changed")
	}
	displacedData, displacedInfo, err := readRootedCASRegularFile(publication.root, publication.recoveryName)
	if err != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("read rooted file CAS displaced preimage before rollback: %w", err)
	}
	if err := exchangeRootedCASFiles(publication.root, publication.recoveryName, publication.canonicalName); err != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("atomically restore rooted file CAS preimage: %w", err)
	}
	restoredData, restoredInfo, restoredErr := readRootedCASRegularFile(publication.root, publication.canonicalName)
	preparedData, preparedInfo, preparedErr := readRootedCASRegularFile(publication.root, publication.recoveryName)
	if restoredErr != nil || preparedErr != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("rooted file CAS rollback identity is ambiguous: %w", errors.Join(restoredErr, preparedErr))
	}
	if !os.SameFile(displacedInfo, restoredInfo) || !bytes.Equal(displacedData, restoredData) ||
		!os.SameFile(publication.replacementInfo, preparedInfo) || !bytes.Equal(publication.replacementData, preparedData) {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("rooted file CAS rollback identity is ambiguous")
	}
	if err := syncRootedCASDirectory(publication.root); err != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("sync restored rooted file CAS preimage: %w", err)
	}
	if err := publication.root.Remove(publication.recoveryName); err != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("remove rolled-back rooted file CAS postimage: %w", err)
	}
	if err := syncRootedCASDirectory(publication.root); err != nil {
		publication.state = RootedFileCASAmbiguous
		return fmt.Errorf("sync rooted file CAS rollback cleanup: %w", err)
	}
	publication.state = RootedFileCASNotPublished
	publication.resolved = true
	return nil
}

func (publication *RootedFileCAS) failAndRollback(cause error) (*RootedFileCAS, error) {
	if err := publication.Rollback(); err != nil {
		publication.state = RootedFileCASAmbiguous
		return publication, fmt.Errorf("%w; rooted file CAS rollback requires manual repair: %v", cause, err)
	}
	return publication, cause
}

func (publication *RootedFileCAS) captureExchangedFiles() error {
	canonicalData, canonicalInfo, err := readRootedCASRegularFile(publication.root, publication.canonicalName)
	if err != nil {
		return fmt.Errorf("read rooted file CAS published postimage: %w", err)
	}
	if !os.SameFile(publication.replacementInfo, canonicalInfo) || !bytes.Equal(publication.replacementData, canonicalData) {
		return fmt.Errorf("rooted file CAS published postimage changed during exchange")
	}
	displacedData, displacedInfo, err := readRootedCASRegularFile(publication.root, publication.recoveryName)
	if err != nil {
		return fmt.Errorf("read rooted file CAS displaced preimage: %w", err)
	}
	publication.displacedData = displacedData
	publication.displacedInfo = displacedInfo
	return nil
}

func (publication *RootedFileCAS) validateExchangedPublication() error {
	canonicalData, canonicalInfo, err := readRootedCASRegularFile(publication.root, publication.canonicalName)
	if err != nil {
		return err
	}
	recoveryData, recoveryInfo, err := readRootedCASRegularFile(publication.root, publication.recoveryName)
	if err != nil {
		return err
	}
	if !os.SameFile(publication.replacementInfo, canonicalInfo) || !bytes.Equal(publication.replacementData, canonicalData) ||
		!os.SameFile(publication.displacedInfo, recoveryInfo) || !bytes.Equal(publication.displacedData, recoveryData) {
		return fmt.Errorf("rooted file CAS publication changed before resolution")
	}
	return nil
}

func (publication *RootedFileCAS) validateLinkedPublication() error {
	canonicalData, canonicalInfo, err := readRootedCASRegularFile(publication.root, publication.canonicalName)
	if err != nil {
		return err
	}
	recoveryData, recoveryInfo, err := readRootedCASRegularFile(publication.root, publication.recoveryName)
	if err != nil {
		return err
	}
	if !os.SameFile(publication.replacementInfo, canonicalInfo) || !os.SameFile(canonicalInfo, recoveryInfo) ||
		!bytes.Equal(publication.replacementData, canonicalData) || !bytes.Equal(publication.replacementData, recoveryData) {
		return fmt.Errorf("conditionally published rooted file changed before resolution")
	}
	return nil
}

func validateRootedCASLeaf(name string) error {
	if name == "" || name == "." || filepath.Base(name) != name || !filepath.IsLocal(name) {
		return fmt.Errorf("name must be a local leaf")
	}
	return nil
}

func readRootedCASRegularFile(root *os.Root, name string) ([]byte, os.FileInfo, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("rooted file CAS entry must be a regular file: %s", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	openedAfter, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, openedAfter) || !os.SameFile(openedAfter, after) ||
		int64(len(data)) != openedAfter.Size() {
		return nil, nil, fmt.Errorf("rooted file CAS entry changed while read: %s", name)
	}
	return data, openedAfter, nil
}

func exchangeRootedCASFiles(root *os.Root, leftName, rightName string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("rooted file CAS parent is not a directory")
	}
	return exchangeRootedCASAt(int(dir.Fd()), leftName, rightName)
}

func syncRootedCASDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) {
		return syncErr
	}
	return closeErr
}
