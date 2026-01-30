package controller

import (
	"errors"
	"sync"
)

// TaskQueue manages a FIFO queue of pending tasks per app.
type TaskQueue struct {
	mu    sync.Mutex
	tasks map[string][]PendingTask // map[AppID][]PendingTask
}

// NewTaskQueue creates a new task queue.
func NewTaskQueue() *TaskQueue {
	return &TaskQueue{
		tasks: make(map[string][]PendingTask),
	}
}

// Enqueue adds a task to the queue for a specific app.
func (q *TaskQueue) Enqueue(task PendingTask) {
	q.mu.Lock()
	defer q.mu.Unlock()
	
	q.tasks[task.AppID] = append(q.tasks[task.AppID], task)
}

// Dequeue retrieves and removes the next task for a specific app.
func (q *TaskQueue) Dequeue(appID string) (*PendingTask, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	tasks, exists := q.tasks[appID]
	if !exists || len(tasks) == 0 {
		return nil, errors.New("queue is empty")
	}

	task := tasks[0]
	q.tasks[appID] = tasks[1:]
	return &task, nil
}

// Peek returns the next task without removing it.
func (q *TaskQueue) Peek(appID string) (*PendingTask, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	tasks, exists := q.tasks[appID]
	if !exists || len(tasks) == 0 {
		return nil, errors.New("queue is empty")
	}

	return &tasks[0], nil
}

// Size returns the number of pending tasks for an app.
func (q *TaskQueue) Size(appID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.tasks[appID])
}
