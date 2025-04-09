package inspect

import (
	"math"
	"sync"
	"time"

	rbt "github.com/emirpasic/gods/trees/redblacktree"
	"github.com/emirpasic/gods/utils"
)

type TaskType uint8

const (
	Heartbeat TaskType = iota
	InspectTimeout
	Function
)

type Task struct {
	T     TaskType //Type
	Value interface{}
	Time  int64
}

type TimeTree struct {
	tree  *rbt.Tree
	timer *time.Timer
	next  int64
	mutex sync.Mutex
	c     chan struct{}
}

func NewTimeTree() *TimeTree {
	return &TimeTree{
		tree:  rbt.NewWith(utils.Int64Comparator),
		timer: time.NewTimer(0),
		c:     make(chan struct{}),
		next:  math.MaxInt64,
	}
}

func (t *TimeTree) Run(f func(*Task)) {
	for {
		select {
		case <-t.timer.C:

			t.mutex.Lock()
			for node := t.tree.Left(); node != nil; node = t.tree.Left() {

				nsec := node.Key.(int64)

				if time.Now().Before(time.Unix(0, nsec)) {
					t.timer.Reset(time.Until(time.Unix(0, nsec)))
					break
				}

				// We need to unlock here to prevent a deadlock in case the task issues a new task
				// Rare case where you unlock and then lock as opposed to locking and unlocking
				t.mutex.Unlock()
				task := node.Value.(*Task)
				f(task)
				t.mutex.Lock()
				t.tree.Remove(nsec)
			}
			t.mutex.Unlock()
		case <-t.c:
			return
		}
	}
}

func (t *TimeTree) Stop() {
	t.c <- struct{}{}
}

func (t *TimeTree) AddTask(task *Task) {
	t.mutex.Lock()

	// Check rare case where a task is scheduled at the exact same nanosecond
	for _, ok := t.tree.Get(task.Time); ok; _, ok = t.tree.Get(task.Time) {
		task.Time++
	}

	t.tree.Put(task.Time, task)
	t.resetTimer()
	t.mutex.Unlock()
}

func (t *TimeTree) RemoveTask(n int64) {
	t.mutex.Lock()
	t.tree.Remove(n)
	t.resetTimer()
	t.mutex.Unlock()
}

func (t *TimeTree) resetTimer() {
	node := t.tree.Left()
	if node == nil {
		return
	}

	t.timer.Reset(time.Until(time.Unix(0, node.Key.(int64))))
}
