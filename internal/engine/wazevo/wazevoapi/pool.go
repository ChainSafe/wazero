package wazevoapi

const poolPageSize = 128

// Pool is a pool of T that can be allocated and reset.
// This is useful to avoid unnecessary allocations.
type Pool[T any] struct {
	pages            []*[poolPageSize]T
	resetFn          func(*T)
	allocated, index int
}

// NewPool returns a new Pool.
// resetFn is called when a new T is allocated in Pool.Allocate.
func NewPool[T any](resetFn func(*T)) Pool[T] {
	var ret Pool[T]
	ret.resetFn = resetFn
	ret.Reset()
	return ret
}

// Allocated returns the number of allocated T currently in the pool.
func (p *Pool[T]) Allocated() int {
	return p.allocated
}

// Allocate allocates a new T from the pool.
func (p *Pool[T]) Allocate() *T {
	if p.index == poolPageSize {
		if len(p.pages) == cap(p.pages) {
			p.pages = append(p.pages, new([poolPageSize]T))
		} else {
			i := len(p.pages)
			p.pages = p.pages[:i+1]
			if p.pages[i] == nil {
				p.pages[i] = new([poolPageSize]T)
			}
		}
		p.index = 0
	}
	ret := &p.pages[len(p.pages)-1][p.index]
	if p.resetFn != nil {
		p.resetFn(ret)
	}
	p.index++
	p.allocated++
	return ret
}

// View returns the pointer to i-th item from the pool.
func (p *Pool[T]) View(i int) *T {
	page, index := i/poolPageSize, i%poolPageSize
	return &p.pages[page][index]
}

// Reset resets the pool.
func (p *Pool[T]) Reset() {
	p.pages = p.pages[:0]
	p.index = poolPageSize
	p.allocated = 0
}

// IDedPool is a pool of T that can be allocated and reset, with a way to get T by an ID.
type IDedPool[T any] struct {
	pool             Pool[T]
	idToItems        []*T
	maxIDEncountered int
}

// NewIDedPool returns a new IDedPool.
func NewIDedPool[T any](resetFn func(*T)) IDedPool[T] {
	return IDedPool[T]{pool: NewPool[T](resetFn)}
}

// GetOrAllocate returns the T with the given id.
func (p *IDedPool[T]) GetOrAllocate(id int) *T {
	if p.maxIDEncountered < id {
		p.maxIDEncountered = id
	}
	if id >= len(p.idToItems) {
		p.idToItems = append(p.idToItems, make([]*T, id-len(p.idToItems)+1)...)
	}
	if p.idToItems[id] == nil {
		p.idToItems[id] = p.pool.Allocate()
	}
	return p.idToItems[id]
}

// Get returns the T with the given id, or nil if it's not allocated.
func (p *IDedPool[T]) Get(id int) *T {
	if id >= len(p.idToItems) {
		return nil
	}
	return p.idToItems[id]
}

// Reset resets the pool.
func (p *IDedPool[T]) Reset() {
	p.pool.Reset()
	for i := range p.idToItems {
		p.idToItems[i] = nil
	}
	p.maxIDEncountered = -1
}

// MaxIDEncountered returns the maximum id encountered so far.
func (p *IDedPool[T]) MaxIDEncountered() int {
	return p.maxIDEncountered
}

const arraySize = 20

// VarLengthPool is a pool of VarLength[T] that can be allocated and reset.
type VarLengthPool[T any] struct {
	arrayPool Pool[[arraySize]T]
	slicePool Pool[[]T]
}

// VarLength is a variable length array that can be reused via a pool.
type VarLength[T any] struct {
	backing *[]T
}

// NewVarLengthPool returns a new VarLengthPool.
func NewVarLengthPool[T any]() VarLengthPool[T] {
	return VarLengthPool[T]{
		arrayPool: NewPool[[arraySize]T](nil),
		slicePool: NewPool[[]T](func(i *[]T) {
			*i = (*i)[:0]
		}),
	}
}

// NewNilVarLength returns a new VarLength[T] with a nil backing.
func NewNilVarLength[T any]() VarLength[T] {
	var v *[]T = nil
	return VarLength[T]{backing: v}
}

// Allocate allocates a new VarLength[T] from the pool.
func (p *VarLengthPool[T]) Allocate(knownMin int) VarLength[T] {
	if knownMin <= arraySize {
		arr := p.arrayPool.Allocate()[:0]
		return VarLength[T]{backing: &arr}
	}
	slc := p.slicePool.Allocate()
	return VarLength[T]{backing: slc}
}

// Reset resets the pool.
func (p *VarLengthPool[T]) Reset() {
	p.arrayPool.Reset()
	p.slicePool.Reset()
}

// Append appends an item to the backing slice.
func (i VarLength[T]) Append(p *VarLengthPool[T], item T) VarLength[T] {
	if i.backing == nil {
		arr := p.arrayPool.Allocate()[:0]
		i.backing = &arr
	}

	if len(*i.backing)+1 <= arraySize {
		*i.backing = append(*i.backing, item)
		return i
	} else if len(*i.backing) == arraySize {
		slc := p.slicePool.Allocate()
		// Copy the array to the slice.
		*slc = append(*slc, *i.backing...)
	}
	*i.backing = append(*i.backing, item)
	return i
}

// View returns the backing slice.
func (i VarLength[T]) View() []T {
	if i.backing == nil {
		return nil
	}
	return *i.backing
}

// Cut cuts the backing slice to the given length.
// Precondition: n <= len(i.backing).
func (i VarLength[T]) Cut(n int) {
	*i.backing = (*i.backing)[:n]
}
