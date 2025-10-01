use crate::tasks::*;
use crate::ws::client::WebSocket;
use std::time::Duration;
use tokio::runtime::Runtime;
use tracing::{error, info, warn};

use tokio::runtime;

pub struct Service {
    state: State,
}

async fn build_tasks() -> anyhow::Result<Tasks, anyhow::Error> {
    let mut tasks = Tasks::new();

    let ws = WebSocket::new()?;

    tasks.register(ws);
    Ok(tasks)
}

enum State {
    Stopped,
    Running {
        shutdown_handle: ShutdownHandle,
        runtime: Runtime,
    },
}

impl Service {
    pub fn new() -> Self {
        Self {
            state: State::Stopped,
        }
    }

    pub fn start(&mut self) -> anyhow::Result<()> {
        let runtime = runtime::Builder::new_multi_thread()
            .enable_all()
            .build()
            .expect("failed to create runtime");

        let tasks = runtime.block_on(build_tasks())?;

        let mut join_all = futures::future::select_all(
            tasks.inner.into_iter().map(|child| Box::pin(child.join())),
        );

        runtime.spawn(async {
            loop {
                let (result, _, rest) = join_all.await;
                match result {
                    Ok(Ok(())) => {
                        println!("A task terminated gracefully")
                    }
                    Ok(Err(error)) => error!("A task failed {:?}", error),
                    Err(error) => error!("Something went very wrong with a task {:?}", error),
                }

                if rest.is_empty() {
                    break;
                } else {
                    join_all = futures::future::select_all(rest);
                }
            }
        });

        self.state = State::Running {
            shutdown_handle: tasks.shutdown_handle,
            runtime,
        };
        Ok(())
    }

    pub fn stop(&mut self) {
        match std::mem::replace(&mut self.state, State::Stopped) {
            State::Stopped => {
                info!("Gateway service is already stopped");
            }
            State::Running {
                shutdown_handle,
                runtime,
            } => {
                error!("Stopping gateway service...");

                // Send shutdown signals to all tasks
                shutdown_handle.signal();

                runtime.block_on(async move {
                    const MAX_COUNT: usize = 3;

                    let all_closed = shutdown_handle.all_closed();
                    tokio::pin!(all_closed);

                    for attempt in 1..=MAX_COUNT {
                        let timeout = tokio::time::sleep(Duration::from_secs(5));
                        tokio::pin!(timeout);

                        tokio::select! {
                            _ = &mut all_closed => {
                                info!("All tasks have shut down gracefully.");
                                return;
                            }
                            _ = &mut timeout => {
                                warn!("Timeout waiting for tasks to shut down (attempt {}/{})", attempt, MAX_COUNT);
                            }
                        }
                    }
                });

                self.state = State::Stopped;
            }
        }
    }
}
