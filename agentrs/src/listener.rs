use crate::tasks::tasks::*;
use crate::ws::client::WebSocket;
use std::time::Duration;
use tokio::runtime::Runtime;
use tracing::{error, info};

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

                //get the tasks name
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
                    let mut count = 0;

                    loop {
                        tokio::select! {
                            _ = shutdown_handle.all_closed() => {
                                info!("All tasks have terminated gracefully");
                                break;
                            }
                            _ = tokio::time::sleep(Duration::from_secs(10)) => {
                                count += 1;

                                if count >= MAX_COUNT {
                                    error!("Some tasks are not terminating, forcing shutdown");
                                    break;
                                } else {
                                    error!("Waiting for tasks to terminate... (attempt {}/{})", count, MAX_COUNT);
                                }
                            }
                        }
                    }
                });

                // Wait for 1 more second before forcefully shutting down the runtime
                runtime.shutdown_timeout(Duration::from_secs(1));

                self.state = State::Stopped;
            }
        }
    }
}
