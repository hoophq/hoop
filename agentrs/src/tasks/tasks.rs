use std::future::Future;

use async_trait::async_trait;
use tokio::task::JoinHandle;

#[derive(Debug)]
pub struct ShutdownHandle(tokio::sync::watch::Sender<()>);

impl ShutdownHandle {
    pub fn new() -> (Self, ShutdownSignal) {
        let (sender, receiver) = tokio::sync::watch::channel(());
        (Self(sender), ShutdownSignal(receiver))
    }

    pub fn signal(&self) {
        let _ = self.0.send(());
    }

    pub async fn all_closed(&self) {
        self.0.closed().await;
    }
}

#[derive(Clone, Debug)]
pub struct ShutdownSignal(tokio::sync::watch::Receiver<()>);

impl ShutdownSignal {
    pub async fn wait(&mut self) {
        let _ = self.0.changed().await;
    }
}

/// Aborts the running task when dropped.
#[must_use]
pub struct ChildTask<T>(JoinHandle<T>);

impl<T> ChildTask<T> {
    pub fn spawn<F>(future: F) -> Self
    where
        F: Future<Output = T> + Send + 'static,
        T: Send + 'static,
    {
        ChildTask(tokio::task::spawn(future))
    }

    pub async fn join(mut self) -> Result<T, tokio::task::JoinError> {
        (&mut self.0).await
    }

    /// Immediately abort the task
    pub fn abort(&self) {
        self.0.abort()
    }

    /// Drop without aborting the task
    pub fn detach(self) {
        core::mem::forget(self);
    }
}

impl<T> From<JoinHandle<T>> for ChildTask<T> {
    fn from(value: JoinHandle<T>) -> Self {
        Self(value)
    }
}

impl<T> Drop for ChildTask<T> {
    fn drop(&mut self) {
        self.abort();
    }
}

#[async_trait]
pub trait Task {
    type Output: Send;

    const NAME: &'static str;

    async fn run(self, shutdown_signal: ShutdownSignal) -> Self::Output;
}

fn spawn_task<T>(task: T, shutdown_signal: ShutdownSignal) -> ChildTask<T::Output>
where
    T: Task + 'static,
{
    let task_fut = task.run(shutdown_signal);
    let handle = spawn_task_impl(task_fut, T::NAME);
    ChildTask(handle)
}

fn spawn_task_impl<T>(future: T, _name: &str) -> JoinHandle<T::Output>
where
    T: Future + Send + 'static,
    T::Output: Send + 'static,
{
    tokio::task::spawn(future)
}

pub struct Tasks {
    pub inner: Vec<ChildTask<anyhow::Result<()>>>,
    pub shutdown_handle: ShutdownHandle,
    pub shutdown_signal: ShutdownSignal,
}

impl Tasks {
    pub fn new() -> Self {
        let (shutdown_handle, shutdown_signal) = ShutdownHandle::new();

        Self {
            inner: Vec::new(),
            shutdown_handle,
            shutdown_signal,
        }
    }

    pub fn register<T>(&mut self, task: T)
    where
        T: Task<Output = anyhow::Result<()>> + 'static,
    {
        let child = spawn_task(task, self.shutdown_signal.clone());
        self.inner.push(child);
    }
}
