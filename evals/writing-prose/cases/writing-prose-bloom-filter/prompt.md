Write the explainer section for a database internals guide, for a developer deciding whether to put a Bloom filter in front of a slow lookup. The question it answers: What is a Bloom filter, and when is it worth putting in front of a slow lookup?

Facts, each checked against the original paper and the LevelDB source:

- A Bloom filter is a fixed-size array of bits plus k hash functions. It answers whether a key is in a set.
- Adding a key sets the k bits its hashes point to.
- Checking a key reads those k bits. If any is 0 the key was never added. If all are 1 it probably was.
- It never gives a false negative. It can give a false positive, because other keys may have set the same bits.
- The false positive rate rises as the array fills. About 10 bits per key with 7 hashes gives a rate near 1%.
- Double hashing derives all k bit positions from two hash values, so the filter needs only two real hash functions.
- The best k for a given array size and key count is bits per key times ln 2.
- A filter cannot remove a key, because clearing a bit could erase another key that shares it.
- A counting variant replaces each bit with a small counter so that keys can be removed.
- A filter cannot list its keys or return a value.
- LevelDB keeps one filter per file on disk, so a read skips every file whose filter says no.

Write the final section to section.md.
