package exec

// TODO: syscall.SIGWINCH is not implemented on windows!
// const SIGWINCH = syscall.Signal(28)

// type Command struct {
// 	cmd                *exec.Cmd
// 	onPreCommandHookFn func() error
// }

// func Run(stream pb.Transport_ConnectClient, args []string, loader *spinner.Spinner, cleanUpFn func()) {
// 	loader.Color("green")
// 	info, err := os.Stdin.Stat()
// 	if err != nil {
// 		panic(err)
// 	}
// 	spec := map[string][]byte{}
// 	if len(args) > 0 {
// 		encArgs, err := pb.GobEncode(args)
// 		if err != nil {
// 			log.Fatalf("failed encoding args, err=%v", err)
// 		}
// 		spec[string(pb.SpecClientExecArgsKey)] = encArgs
// 	}

// 	ttyMode := true
// 	var output []byte
// 	if info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0 {
// 		ttyMode = false
// 		stdinPipe := os.NewFile(uintptr(syscall.Stdin), "/dev/stdin")
// 		reader := bufio.NewReader(stdinPipe)
// 		for {
// 			input, err := reader.ReadByte()
// 			if err != nil && err == io.EOF {
// 				break
// 			}
// 			output = append(output, input)
// 		}
// 		stdinPipe.Close()
// 		pb.NewStreamWriter(stream.Send, pb.PacketExecRunProcType, spec).
// 			Write([]byte(string(output)))
// 		loader.Suffix = " executing command"
// 	}
// 	ptty, tty, err := pty.Open()
// 	if err != nil {
// 		loader.Stop()
// 		log.Fatal(err)
// 	}
// 	defer ptty.Close()
// 	defer tty.Close()

// 	// Set stdin in raw mode.
// 	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
// 	if err != nil {
// 		loader.Stop()
// 		log.Fatal(err)
// 	}

// 	// Handle pty size.
// 	ch := make(chan os.Signal, 1)
// 	signal.Notify(ch, SIGWINCH, syscall.SIGABRT, syscall.SIGTERM, syscall.SIGINT)
// 	// TODO: make resize to propagate remotely!
// 	go func() {
// 		for {
// 			switch <-ch {
// 			case SIGWINCH:
// 				if err := pty.InheritSize(os.Stdin, ptty); err != nil {
// 					log.Printf("error resizing pty, err=%v", err)
// 				}
// 			case syscall.SIGABRT, syscall.SIGTERM:
// 				loader.Stop()
// 				go func() {
// 					_ = term.Restore(int(os.Stdin.Fd()), oldState)
// 					os.Exit(1)
// 				}()
// 			case syscall.SIGINT:
// 				loader.Stop()
// 				// TODO: check errors
// 				_ = stream.Send(&pb.Packet{Type: pb.PacketExecCloseTermType.String()})
// 				// give some time to return all the data after the interrupt
// 				time.Sleep(time.Second * 4)
// 				cleanUpFn()
// 				os.Exit(130) // ctrl+c exit code
// 			}
// 		}
// 	}()
// 	ch <- SIGWINCH                                // Initial resize.
// 	defer func() { signal.Stop(ch); close(ch) }() // Cleanup signals when done.

// 	if ttyMode {
// 		// Copy stdin to the pty and the pty to stdout.
// 		// NOTE: The goroutine will keep reading until the next keystroke before returning.
// 		go func() {
// 			sw := pb.NewStreamWriter(stream.Send, pb.PacketExecWriteAgentStdinType, spec)
// 			if loader.Enabled() {
// 				_, _ = sw.Write(TermEnterKeyStrokeType)
// 			}
// 			// TODO: check errors
// 			_, _ = io.Copy(sw, os.Stdin)
// 		}()
// 	}

// 	for {
// 		pkt, err := stream.Recv()
// 		if err == io.EOF {
// 			_ = term.Restore(int(os.Stdin.Fd()), oldState)
// 			os.Stdout.Write(TermEnterKeyStrokeType)
// 			break
// 		}
// 		if err != nil {
// 			_ = term.Restore(int(os.Stdin.Fd()), oldState)
// 			os.Stdout.Write(TermEnterKeyStrokeType)
// 			log.Fatalf("closing client proxy, err=%v", err)
// 		}
// 		switch pb.PacketType(pkt.Type) {
// 		case pb.PacketExecCloseTermType:
// 			loader.Stop()
// 			_ = term.Restore(int(os.Stdin.Fd()), oldState)
// 			exitCodeStr := string(pkt.Spec[pb.SpecClientExecExitCodeKey])
// 			exitCode, err := strconv.Atoi(exitCodeStr)
// 			cleanUpFn()
// 			if exitCodeStr == "" || err != nil {
// 				// End with a custom exit code, because we don't
// 				// know what returned from the remote terminal
// 				exitCode = UnknowExecExitCode
// 			}
// 			os.Exit(exitCode)
// 		case pb.PacketExecClientWriteStdoutType:
// 			// 13,10 == \r\n
// 			if loader.Active() && !bytes.Equal(pkt.Payload, []byte{13, 10}) {
// 				loader.Stop()
// 			}
// 			_, _ = os.Stdout.Write(pkt.Payload)
// 		}
// 	}
// }

// type TTY struct {
// 	ptty *os.File
// 	cmd  *Command
// }

// func (t *TTY) Close() (err error) {
// 	if t.ptty != nil {
// 		err = t.ptty.Close()
// 	}
// 	if err != nil {
// 		return err
// 	}
// 	return nil
// 	// return t.cmd.onPostCommandHookFn()
// }

// func (t *TTY) File() *os.File {
// 	return t.ptty
// }

// func (t *TTY) ProcWait() error {
// 	return t.cmd.cmd.Wait()
// }

// func newCommand(
// 	targetType, customCommand string,
// 	rawEnvJSON []byte,
// 	args []string) (*Command, error) {
// 	cred, envs, err := envtypes.New(targetType, rawEnvJSON)
// 	if err != nil {
// 		return nil, err
// 	}
// 	c := &Command{
// 		onPreCommandHookFn:  func() error { return nil },
// 		onPostCommandHookFn: func() error { return nil },
// 	}
// 	envs.Set("HOME", os.Getenv("HOME"))
// 	var cmdList []string
// 	switch targetType {
// 	case "mysql", "mysql-csv":
// 		env, _ := cred.(*envtypes.MySQL)
// 		cmdList = []string{"mysql",
// 			fmt.Sprintf("-h%s", env.Host),
// 			fmt.Sprintf("-u%s", env.Username),
// 			"--port", env.Port}
// 		if env.Database != "" {
// 			cmdList = append(cmdList, fmt.Sprintf("-D%s", env.Database))
// 		}
// 		envs.Set("MYSQL_PWD", env.Password)
// 	case "postgres", "postgres-csv":
// 		env, _ := cred.(*envtypes.Postgres)
// 		cmdList = []string{"psql",
// 			"--no-align",
// 			"--host", env.Host,
// 			"--username", env.Username,
// 			"--port", env.Port,
// 			"-v", "ON_ERROR_STOP=1"}
// 		if env.Database != "" {
// 			cmdList = append(cmdList, fmt.Sprintf("-d%s", env.Database))
// 		}
// 		envs.Set("PG_PASSWORD", env.Password)
// 	case "sql-server":
// 		env, _ := cred.(*envtypes.MSSQL)
// 		cmdList = []string{
// 			"sqlcmd", "-b", "-r",
// 			"-S", env.ConnectionURI,
// 			"-U", env.Username,
// 			"-P", env.Password,
// 			"-d", env.Database}
// 	case "mongodb":
// 		env, _ := cred.(*envtypes.Mongo)
// 		cmdList = []string{
// 			"mongosh",
// 			"--quiet",
// 			env.MongoURI}
// 	case "bash":
// 		cmdList = []string{"bash"}
// 		if customCommand != "" {
// 			cmdList = strings.Split(customCommand, " ")
// 		}
// 	case "python":
// 		cmdList = []string{"python3"}
// 	case "elixir":
// 		cmdList = []string{"elixir"}
// 	case "node":
// 		cmdList = []string{"node"}
// 	case "k8s", "k8s-exec":
// 		env, _ := cred.(*envtypes.Kubernetes)
// 		kubeconfigRaw, err := env.ParseKubeConfigData()
// 		if err != nil {
// 			return nil, err
// 		}
// 		c.onPreCommandHookFn = func() error {
// 			kbConfigRaw := kubeconfigRaw
// 			f, err := os.CreateTemp("", "kubeconfig")
// 			if err != nil {
// 				return fmt.Errorf("failed creating tmp kubeconfig file, err=%v", err)
// 			}
// 			if _, err := f.Write(kbConfigRaw); err != nil {
// 				return err
// 			}
// 			c.preCommandHookFnOutput = []byte(f.Name())
// 			c.cmd.Env = append(c.cmd.Env, fmt.Sprintf("KUBECONFIG=%s", f.Name()))
// 			return err
// 		}
// 		c.onPostCommandHookFn = func() error {
// 			return os.Remove(string(c.preCommandHookFnOutput))
// 		}
// 		cmdList = []string{
// 			"kubectl",
// 			"exec",
// 			"--stdin",
// 			"--tty"}
// 		if targetType == "k8s" {
// 			cmdList = []string{"kubectl"}
// 		}
// 	case "ecs-exec":
// 		env, _ := cred.(*envtypes.ECSExec)
// 		taskArns, err := awsutils.ListECSTasks(
// 			env.Cluster,
// 			env.ServiceName,
// 			env.Region,
// 			credentials.NewStaticCredentials(env.AccessKeyID, env.SecretAccessKey, ""),
// 		)
// 		if err != nil || len(taskArns) == 0 {
// 			return nil, fmt.Errorf("failed listing tasks, err=%v", err)
// 		}
// 		ecsTaskID := taskArns[0]
// 		envs.Set("AWS_REGION", env.Region)
// 		envs.Set("AWS_ACCESS_KEY_ID", env.AccessKeyID)
// 		envs.Set("AWS_SECRET_ACCESS_KEY", env.SecretAccessKey)
// 		cmdList = []string{
// 			"aws",
// 			"ecs",
// 			"execute-command",
// 			"--interactive",
// 			"--cluster", envs.Get("ECS_CLUSTER"),
// 			"--task", *ecsTaskID}
// 	default:
// 		return nil, fmt.Errorf("connection type %v not found", targetType)
// 	}
// 	cmdList = append(cmdList, args...)
// 	argList := []string{}
// 	if len(cmdList) > 1 {
// 		argList = cmdList[1:]
// 	}
// 	c.cmd = exec.Command(cmdList[0], argList...)
// 	c.cmd.Env = envs.ParseToEnvExec()
// 	c.cmd.Env = append(c.cmd.Env, fmt.Sprintf("PATH=%s", os.Getenv("PATH")))

// 	// c.SysProcAttr = &syscall.SysProcAttr{}
// 	// c.SysProcAttr.Credential = &syscall.Credential{Uid: uid, Gid: gid}
// 	return c, nil
// }

// func newCommand(envVar map[string][]byte)

// func RunProcess(
// 	targetType, customCommand string,
// 	rawEnvJSON, stdinInput []byte,
// 	streamWriter io.Writer,
// 	args []string) (*exec.Cmd, error) {
// 	c := &Command{}
// 	// c, err := newCommand(targetType, customCommand, rawEnvJSON, args)
// 	// if err != nil {
// 	// 	return nil, err
// 	// }
// 	pipeStdout, err := c.cmd.StdoutPipe()
// 	if err != nil {
// 		return nil, err
// 	}
// 	pipeStderr, err := c.cmd.StderrPipe()
// 	if err != nil {
// 		return nil, err
// 	}
// 	var stdin bytes.Buffer
// 	c.cmd.Stdin = &stdin
// 	if err := c.cmd.Start(); err != nil {
// 		return nil, err
// 	}

// 	if _, err := stdin.Write(stdinInput); err != nil {
// 		return nil, err
// 	}
// 	copyBuffer(pipeStdout, streamWriter, 1024)
// 	copyBuffer(pipeStderr, streamWriter, 1024)
// 	return c.cmd, err
// }

// func RunProcessOnTTY(targetType, customCommand string, rawEnvJSON []byte, args []string) (*TTY, error) {
// 	// c := &Command{}
// 	c, err := newCommand(targetType, customCommand, rawEnvJSON, args)
// 	if err != nil {
// 		return nil, err
// 	}
// 	if err := c.onPreCommandHookFn(); err != nil {
// 		return nil, err
// 	}
// 	// Start the command with a pty.
// 	ptmx, err := pty.Start(c.cmd)
// 	if err != nil {
// 		return nil, err
// 	}
// 	t := &TTY{ptty: ptmx, cmd: c}
// 	// Handle pty size.
// 	ch := make(chan os.Signal, 1)
// 	signal.Notify(ch, SIGWINCH)
// 	go func() {
// 		for range ch {
// 			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
// 				log.Printf("error resizing pty: %s", err)
// 			}
// 		}
// 	}()
// 	ch <- SIGWINCH                                // Initial resize.
// 	defer func() { signal.Stop(ch); close(ch) }() // Cleanup signals when done.

// 	return t, nil
// }

// func copyBuffer(reader io.ReadCloser, w io.Writer, bufSize int) {
// 	r := bufio.NewReader(reader)
// 	buf := make([]byte, bufSize)
// 	go func() {
// 		for {
// 			n, err := r.Read(buf[:])
// 			if n > 0 {
// 				if _, err := w.Write(buf[:n]); err != nil {
// 					panic(err)
// 				}
// 				continue
// 			}
// 			if err != nil {
// 				if err == io.EOF {
// 					break
// 				}
// 			}
// 		}
// 	}()
// }
