package main

func main() {
	//p, err := plugin.Open("../plugins/auth.so")
	//if err != nil {
	//	fmt.Println(err)
	//	return
	//}
	//
	//s, err := p.Lookup("Hello")
	//if err != nil {
	//	fmt.Println(err)
	//	return
	//}
	//
	//Hello := s.(func() error)
	//
	//err = Hello()
	//if err != nil {
	//	fmt.Println(err)
	//	return
	//}

	serveHTTP()
}
