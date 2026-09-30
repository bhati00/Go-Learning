package primitves

import "sync"

type Node struct {
	key  int
	val  string
	prev *Node
	next *Node
}

func NewNode() *Node {
	return &Node{}
}

type LruCache struct {
	mu       sync.Mutex
	cacheMap map[int]*Node
	capacity int
	// sentinal nodes
	head *Node
	tail *Node
}

func NewLruCache(capacity int) *LruCache {
	lru := LruCache{
		capacity: capacity,
		cacheMap: make(map[int]*Node, capacity),
		head:     NewNode(),
		tail:     NewNode(),
	}
	lru.head.next = lru.tail
	lru.tail.prev = lru.head
	return &lru
}

func (l *LruCache) RemoveNode(node *Node) {
	node.next.prev = node.prev
	node.prev.next = node.next
}

func (l *LruCache) Append(node *Node) {
	l.head.next.prev = node
	node.next = l.head.next
	l.head.next = node
	node.prev = l.head
}

func (l *LruCache) PushForward(node *Node) {
	// remove from existing position
	l.RemoveNode(node)
	// add to the front
	l.Append(node)

}

// access the cache
func (l *LruCache) get(key int) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if node, ok := l.cacheMap[key]; ok {
		// push forward this node
		l.PushForward(node)
		return node.val, nil
	}
	return "", nil
}

// update the cache
func (l *LruCache) put(key int, val string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if node, ok := l.cacheMap[key]; ok {
		node.val = val
		l.cacheMap[key] = node
		// push forward
		l.PushForward(node)
		return true
	}
	node := NewNode()
	node.key = key
	node.val = val
	// Append
	l.Append(node)
	l.cacheMap[key] = node
	if len(l.cacheMap) > l.capacity {
		lru := l.tail.prev
		l.RemoveNode(lru)
		delete(l.cacheMap, lru.key)
	}

	return true
}

func main() {
	lru := NewLruCache(3)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := i % 5
			if i%2 == 0 {
				lru.put(key, "v")
			} else {
				lru.get(key)
			}
		}(i)
	}
	wg.Wait()
}

// Reference only: the idiomatic-Go alternative to the hand-rolled Node/prev/next
// list above, using the standard library's container/list. Needs "container/list"
// imported alongside "sync" if ever uncommented.
/*
type entry struct {
	key   int
	value string
}

type LRUCacheStd struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List            // front = MRU, back = LRU
	items    map[int]*list.Element // key -> node in ll
}

func NewLRUCacheStd(capacity int) *LRUCacheStd {
	return &LRUCacheStd{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[int]*list.Element, capacity),
	}
}

func (c *LRUCacheStd) Get(key int) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return "", false
	}
	c.ll.MoveToFront(elem) // read counts as use — promote to MRU
	return elem.Value.(*entry).value, true
}

func (c *LRUCacheStd) Put(key int, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		elem.Value.(*entry).value = value // update existing key — no size change
		c.ll.MoveToFront(elem)
		return
	}

	elem := c.ll.PushFront(&entry{key: key, value: value})
	c.items[key] = elem

	if c.ll.Len() > c.capacity {
		oldest := c.ll.Back() // back = LRU node
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*entry).key)
	}
}
*/
