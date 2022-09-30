package main

func main() {
	// envStore, err := pexec.NewEnvVarStore(map[string]string{
	// 	"envvar:PGPASSWORD":     "MTIz",
	// 	"envvar:USER":           "Ym9i",
	// 	"envvar:DB":             "dGVzdGRi",
	// 	"envvar:PORT":           "NTQ0NA==",
	// 	"envvar:HOST":           "MTkyLjE2OC4xNS40OA==",
	// 	"filesystem:KUBECONFIG": "bXlrdWJlY29uZmlnZW5j",
	// })
	// if err != nil {
	// 	panic(err)
	// }

	// // fmt.Println(envStore.ParseToKeyVal())
	// // foo := "foo:bar:zinza"
	// // fmt.Printf("%#v\n", strings.Split(foo, ":"))

	// // keyVal := os.Expand(cmd, envStore.getEnvKey)
	// cmdList := []string{
	// 	"psql", "-h$HOST", "--port=$PORT", "-U$USER", "$DB", "-c", "SELECT NOW()",
	// 	// "-W$FOO", "-Z$BAR",
	// }
	// // clientArgs := []string{"--debug $FOO"}
	// cmd2, err := pexec.NewCommand(envStore, cmdList...)
	// if err != nil {
	// 	panic(err)
	// }

	// fmt.Println(cmd2.OnPreExec())
	// // cmd2.OnPreExec()
	// fmt.Printf("ENVS->>> %#v\n", cmd2.Environ())
	// fmt.Printf("CMD->>> %#v\n", cmd2.String())

	// fmt.Println(cmd2.OnPostExec())
	// expandedenv := os.Expand("-W$FOO", func(k string) string { return "" })
	// fmt.Println(expandedenv)

	// for _, cmd := range cmdList {
	// if envKey := envStore.LookupEnvForKey(cmd); envKey != nil {
	// 	fmt.Println("FOUND->>", *envKey)
	// }
	// val := os.Expand(cmd, envStore.Getenv)

	// fmt.Printf("%#v\n", val)
	// }
	// cmdList = pexec.ExpandEnvVarToCmd(envStore, cmdList)
	// cmdName := "/opt/homebrew/opt/libpq/bin/psql"
	// c := exec.Command(cmdName, cmdList...)
	// c.Env = envStore.ParseToKeyVal()
	// fmt.Println(c.String())
	// res, err := c.CombinedOutput()
	// fmt.Println(string(res), err)
}
