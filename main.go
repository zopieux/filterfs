package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	ignore "github.com/sabhiram/go-gitignore"
	"golang.org/x/sys/unix"
)

var (
	roMode = flag.Bool("ro", true, "Mount filesystem as read-only (default)")
	rwMode = flag.Bool("rw", false, "Mount filesystem as read-write")
)

type filterNode struct {
	fs.LoopbackNode
	gitign *atomic.Pointer[ignore.GitIgnore]
}

var _ = (fs.NodeLookuper)((*filterNode)(nil))

func (n *filterNode) gitignore() *ignore.GitIgnore {
	return n.Root().Operations().(*filterNode).gitign.Load()
}

func (n *filterNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := n.relativePath(name)
	gitign := n.gitignore()
	if gitign != nil && gitign.MatchesPath(rel) {
		return nil, syscall.ENOENT
	}
	return n.LoopbackNode.Lookup(ctx, name, out)
}

func (n *filterNode) relativePath(name string) string {
	rel := n.Path(n.Root())
	return filepath.Join(rel, name)
}

// Read handlers.

func (n *filterNode) OpendirHandle(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	stream, errno := n.LoopbackNode.Readdir(ctx)
	if errno != 0 {
		return nil, 0, errno
	}

	var filtered []fuse.DirEntry
	dirRel := n.Path(n.Root())
	gitign := n.gitignore()

	for stream.HasNext() {
		entry, err := stream.Next()
		if err != 0 {
			continue
		}
		rel := filepath.Join(dirRel, entry.Name)
		if gitign == nil || !gitign.MatchesPath(rel) {
			filtered = append(filtered, entry)
		}
	}
	return fs.NewListDirStream(filtered), 0, 0
}

func (n *filterNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	stream, errno := n.LoopbackNode.Readdir(ctx)
	if errno != 0 {
		return nil, errno
	}

	var filtered []fuse.DirEntry
	dirRel := n.Path(n.Root())
	gitign := n.gitignore()

	for stream.HasNext() {
		entry, err := stream.Next()
		if err != 0 {
			continue
		}
		rel := filepath.Join(dirRel, entry.Name)
		if gitign == nil || !gitign.MatchesPath(rel) {
			filtered = append(filtered, entry)
		}
	}
	return fs.NewListDirStream(filtered), 0
}

// Write handlers.

func (n *filterNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	rel := n.relativePath(name)
	gitign := n.gitignore()
	if gitign != nil && gitign.MatchesPath(rel) {
		return nil, nil, 0, syscall.EPERM
	}
	return n.LoopbackNode.Create(ctx, name, flags, mode, out)
}

func (n *filterNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := n.relativePath(name)
	gitign := n.gitignore()
	if gitign != nil && gitign.MatchesPath(rel) {
		return nil, syscall.EPERM
	}
	return n.LoopbackNode.Mkdir(ctx, name, mode, out)
}

func (n *filterNode) Mknod(ctx context.Context, name string, mode, rdev uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := n.relativePath(name)
	gitign := n.gitignore()
	if gitign != nil && gitign.MatchesPath(rel) {
		return nil, syscall.EPERM
	}
	return n.LoopbackNode.Mknod(ctx, name, mode, rdev, out)
}

func (n *filterNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := n.relativePath(name)
	gitign := n.gitignore()
	if gitign != nil && gitign.MatchesPath(rel) {
		return nil, syscall.EPERM
	}
	return n.LoopbackNode.Symlink(ctx, target, name, out)
}

func (n *filterNode) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := n.relativePath(name)
	gitign := n.gitignore()
	if gitign != nil && gitign.MatchesPath(rel) {
		return nil, syscall.EPERM
	}
	return n.LoopbackNode.Link(ctx, target, name, out)
}

func (n *filterNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	relOld := n.relativePath(name)
	var newRel string
	if pNode, ok := newParent.(*filterNode); ok {
		newRel = pNode.relativePath(newName)
	} else {
		newRel = filepath.Join(newParent.EmbeddedInode().Path(n.Root()), newName)
	}

	gitign := n.gitignore()
	if gitign != nil && (gitign.MatchesPath(relOld) || gitign.MatchesPath(newRel)) {
		return syscall.EPERM
	}
	return n.LoopbackNode.Rename(ctx, name, newParent, newName, flags)
}

func (n *filterNode) Unlink(ctx context.Context, name string) syscall.Errno {
	rel := n.relativePath(name)
	gitign := n.gitignore()
	if gitign != nil && gitign.MatchesPath(rel) {
		return syscall.ENOENT
	}
	return n.LoopbackNode.Unlink(ctx, name)
}

func (n *filterNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	rel := n.relativePath(name)
	gitign := n.gitignore()
	if gitign != nil && gitign.MatchesPath(rel) {
		return syscall.ENOENT
	}
	return n.LoopbackNode.Rmdir(ctx, name)
}

func newFilterNode(data *fs.LoopbackRoot, p *fs.Inode, name string, st *syscall.Stat_t) fs.InodeEmbedder {
	n := &filterNode{}
	n.LoopbackNode.RootData = data
	return n
}

func watchIgnoreFile(ignorefile string, gitignPtr *atomic.Pointer[ignore.GitIgnore]) {
	fd, err := unix.InotifyInit()
	if err != nil {
		log.Printf("failed to initialize inotify: %v", err)
		return
	}

	wd, err := unix.InotifyAddWatch(fd, ignorefile, unix.IN_CLOSE_WRITE|unix.IN_MODIFY|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF)
	if err != nil {
		log.Printf("failed to add inotify watch for %s: %v", ignorefile, err)
		unix.Close(fd)
		return
	}

	go func() {
		defer unix.Close(fd)
		var buf [4096]byte
		for {
			n, err := unix.Read(fd, buf[:])
			if err != nil {
				break
			}

			var offset uint32
			hasChange := false
			hasDeleteOrMove := false

			for offset < uint32(n) {
				if offset+unix.SizeofInotifyEvent > uint32(n) {
					break
				}
				// Parse inotify event from buffer.
				event := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))

				mask := event.Mask
				if mask&(unix.IN_MODIFY|unix.IN_CLOSE_WRITE) != 0 {
					hasChange = true
				}
				if mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF) != 0 {
					hasChange = true
					hasDeleteOrMove = true
				}

				offset += unix.SizeofInotifyEvent + event.Len
			}

			if hasChange {
				newGitign, err := ignore.CompileIgnoreFile(ignorefile)
				if err != nil {
					log.Printf("failed to compile reloaded ignore file: %v", err)
				} else {
					gitignPtr.Store(newGitign)
					log.Printf("successfully reloaded ignore file: %s", ignorefile)
				}

				if hasDeleteOrMove {
					// Re-establish the watch on the new file/inode.
					unix.InotifyRmWatch(fd, uint32(wd))
					newWd, err := unix.InotifyAddWatch(fd, ignorefile, unix.IN_CLOSE_WRITE|unix.IN_MODIFY|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF)
					if err == nil {
						wd = newWd
					}
				}
			}
		}
	}()
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 3 {
		log.Fatalf("usage: filterfs [--ro/--rw] <source> <mountpoint> <ignorefile>")
	}
	source := args[0]
	mountpoint := args[1]
	ignorefile := args[2]

	gitign, err := ignore.CompileIgnoreFile(ignorefile)
	if err != nil {
		log.Fatal(err)
	}

	var gitignPtr atomic.Pointer[ignore.GitIgnore]
	gitignPtr.Store(gitign)

	watchIgnoreFile(ignorefile, &gitignPtr)

	var st syscall.Stat_t
	if err := syscall.Stat(source, &st); err != nil {
		log.Fatal(err)
	}

	root := &filterNode{
		gitign: &gitignPtr,
	}
	root.LoopbackNode.RootData = &fs.LoopbackRoot{
		NewNode: newFilterNode,
		Path:    source,
		Dev:     uint64(st.Dev),
	}
	root.LoopbackNode.RootData.RootNode = root

	opts := &fs.Options{}
	opts.Name = "filterfs"
	opts.FsName = "filterfs"
	opts.DirectMount = true

	isReadOnly := true
	if *rwMode {
		isReadOnly = false
	} else if *roMode {
		isReadOnly = true
	}

	if isReadOnly {
		opts.MountOptions.Options = append(opts.MountOptions.Options, "ro")
	}
	
	os.MkdirAll(mountpoint, 0755)
	server, err := fs.Mount(mountpoint, root, opts)
	if err != nil {
		log.Fatal(err)
	}
	defer server.Unmount()

	// Self-unmount at exit.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigChan
		log.Println("Received termination signal, unmounting...")
		server.Unmount()
	}()

	server.Wait()
}
