package sorted

import (
	"io"
	"github.com/armon/go-radix"
	"github.com/pointc-io/ipdb/db/data/btree"
	"github.com/pointc-io/ipdb/db/data/rtree"
)

type Index interface {
	Memory() uint64

	Length() uint64
}

type IndexIterator func(key IndexItem) bool

type IndexType uint8

const (
	BTree   IndexType = 0
	RTree   IndexType = 1
	RadTree IndexType = 2
)

// idx represents a b-tree or r-tree idx and also acts as the
// b-tree/r-tree context for itself.
type SetIndex struct {
	db      *Set // the origin database
	t       IndexType
	btr     *btree.BTree // contains the items
	rtr     *rtree.RTree // contains the items
	radtr   *radix.Tree
	name    string // name of the idx
	pattern string // a required key pattern

	length uint64
	memory uint64

	indexer Indexer
}

func (i *SetIndex) Length() int {
	if i.btr != nil {
		return i.btr.Len()
	} else if i.rtr != nil {
		return i.rtr.Count()
	} else {
		return 0
	}
}

func (i *SetIndex) remove(item IndexItem) IndexItem {
	if i.btr != nil {
		r := i.btr.Delete(item)
		if r != nil {
			return r.(IndexItem)
		} else {
			return nil
		}
	} else if i.rtr != nil {
		i.rtr.Remove(item)
		return nil
	}
	return nil
}

// Snapshot of an idx only includes meta-data to re-create the idx since
// it is built from the items in the tree.
func (i *SetIndex) Snapshot(writer io.Writer) error {
	return nil
}

func (i *SetIndex) Restore(writer io.Writer) error {
	return nil
}

// match matches the pattern to the key
func (idx *SetIndex) match(key Key) bool {
	return key.Match(idx.pattern)
}

// clearCopy creates a copy of the idx, but with an empty dataset.
func (idx *SetIndex) clearCopy() *SetIndex {
	// copy the idx meta information
	nidx := &SetIndex{
		name:    idx.name,
		pattern: idx.pattern,
		db:      idx.db,
		indexer: idx.indexer,
	}
	switch idx.t {
	case BTree:
		idx.btr = btree.New(btreeDegrees, nidx)
	case RTree:
		idx.rtr = rtree.New(nidx)
	}
	return nidx
}

// rebuild rebuilds the idx
// may need to be invoked from a worker if the data set is large
func (idx *SetIndex) rebuild() {
	switch idx.t {
	case BTree:
		idx.btr = btree.New(btreeDegrees, idx)
	case RTree:
		idx.rtr = rtree.New(idx)
	}
	// iterate through all keys and fill the idx
	idx.db.items.Ascend(func(item btree.Item) bool {
		dbi := item.(*Item)
		if !idx.match(dbi.Key) {
			// does not match the pattern, continue
			return true
		}

		var sk IndexItem
		if len(dbi.indexes) > 0 {
			for _, sec := range dbi.indexes {
				secIdx := sec.index()
				if secIdx != nil {
					secIdx.remove(sec)
				}
			}
		}

		switch idx.t {
		case BTree:
			if sk == nil {
				sk = idx.indexer.Index(idx, dbi)
				if sk != nil {
					dbi.indexes = append(dbi.indexes, sk)
				}
			} else {
				idx.btr.Delete(sk)
			}

			if sk != nil {
				idx.btr.ReplaceOrInsert(sk)
			} else {
				// Ignored.
			}

		case RTree:
			if sk == nil {
				sk = idx.indexer.Index(idx, dbi)
				if sk != nil {
					dbi.indexes = append(dbi.indexes, sk)
				}
			} else {
				idx.rtr.Remove(sk)
			}

			if sk != nil {
				idx.rtr.Insert(sk)
			} else {
				// Ignored.
			}
		}
		return true
	})
}

// CreateIndex builds a new idx and populates it with items.
// The items are ordered in an b-tree and can be retrieved using the
// Ascend* and Descend* methods.
// An error will occur if an idx with the same name already exists.
//
// When a pattern is provided, the idx will be populated with
// keys that match the specified pattern. This is a very simple pattern
// match where '*' matches on any number characters and '?' matches on
// any one character.
// The less function compares if string 'a' is less than string 'b'.
// It allows for indexes to create custom ordering. It's possible
// that the strings may be textual or binary. It's up to the provided
// less function to handle the content format and comparison.
// There are some default less function that can be used such as
// IndexString, IndexBinary, etc.
func (db *Set) CreateIndex(name, pattern string, indexer Indexer) error {
	return db.createIndex(BTree, name, pattern, indexer)
}

// CreateSpatialIndex builds a new idx and populates it with items.
// The items are organized in an r-tree and can be retrieved using the
// Intersects method.
// An error will occur if an idx with the same name already exists.
//
// The rect function converts a string to a rectangle. The rectangle is
// represented by two arrays, min and max. Both arrays may have a length
// between 1 and 20, and both arrays must match in length. A length of 1 is a
// one dimensional rectangle, and a length of 4 is a four dimension rectangle.
// There is support for up to 20 dimensions.
// The values of min must be less than the values of max at the same dimension.
// Thus min[0] must be less-than-or-equal-to max[0].
// The IndexRect is a default function that can be used for the rect
// parameter.
func (db *Set) CreateSpatialIndex(name, pattern string, indexer Indexer) error {
	return db.createIndex(RTree, name, pattern, indexer)
}

// createIndex is called by CreateIndex() and CreateSpatialIndex()
func (db *Set) createIndex(
	idxType IndexType,
	name string,
	pattern string,
	indexer Indexer,
) error {
	if name == "" {
		// cannot create an idx without a name.
		// an empty name idx is designated for the main "keys" tree.
		return ErrIndexExists
	}
	// check if an idx with that name already exists.
	if _, ok := db.idxs[name]; ok {
		// idx with name already exists. error.
		return ErrIndexExists
	}

	// intialize new idx
	idx := &SetIndex{
		t:       idxType,
		name:    name,
		pattern: pattern,
		db:      db,
		indexer: indexer,
	}

	// save the idx
	db.insertIndex(idx)

	idx.rebuild()
	return nil
}

// DropIndex removes an idx.
func (db *Set) DropIndex(name string) error {
	if name == "" {
		// cannot drop the default "keys" idx
		return ErrInvalidOperation
	}
	idx, ok := db.idxs[name]
	if !ok {
		return ErrNotFound
	}

	db.removeIndex(idx)

	return nil
}
