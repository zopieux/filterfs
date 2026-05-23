# filterfs

```shell
# Mountpoint
$ mkdir /tmp/mnt
# Ignored file
$ echo secret > result
# Read-only:
$ nix run 'github:zopieux/filterfs' -- --ro . /tmp/mnt .gitignore
# Read/write:
# nix run 'github:zopieux/filterfs' -- --rw . /tmp/mnt .gitignore
$ ls result ; cat result                    # works
$ ls /tmp/mnt/result ; cat /tmp/mnt/result  # should fail
```

### License

GNU General Public License v3.0.
