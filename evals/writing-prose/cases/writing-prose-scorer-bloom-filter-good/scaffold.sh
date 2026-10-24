#!/bin/bash
set -e
cat > canned.md <<'CANNED'
A Bloom filter is a fixed-size array of bits with k hash functions, and it answers whether a key is in a set. Adding a key sets the k bits its hashes point to. Checking a key reads those same bits. If any of them is 0 the key was never added, and if all are 1 it probably was.

The filter never gives a false negative, since an added key always leaves its bits set. It can give a false positive, because other keys may have set the same bits, and the chance rises as the array fills. About 10 bits per key with 7 hashes gives a false positive rate near 1%.

A filter cannot remove a key, because clearing a bit could erase another key that shares it. It cannot list its keys or return a value either. It answers probably-yes or no.

That makes it useful in front of a slow lookup that usually finds nothing. LevelDB keeps one filter per file on disk, so a read skips every file whose filter says no and opens only the files that might hold the key.
CANNED
