package ingresscontroller

type IngressController interface {
	GetName() string
	Run()
	Stop()
	GetLBConfig() interface{}
}

func GetController(name string) IngressController {
	if controller, ok := controllers[name]; ok {
		return controller
	}
	return controllers["kubernetes"]
}

func RegisterController(name string, controller IngressController) error {
	if controllers == nil {
		controllers = make(map[string]Controller)
	}
	if _, exists := controllers[name]; exists {
		return fmt.Errorf("controller already registered")
	}
	controllers[name] = controller
	return nil
}
