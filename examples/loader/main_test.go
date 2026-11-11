package main

import "context"

// The output below is quoted in docs/loader.md.
func Example_run() {
	if err := inTemp(context.Background()); err != nil {
		panic(err)
	}
	// Output:
	// shout: hello, shout! (debug=false)
	// tenant-shout: hola, tenant-shout! (debug=true)
	// -- update greeter
	// shout: goodbye, shout! (debug=false)
	// -- reject an invalid entry
	// loader: entry needs exactly one of "plugin", "plugins" or "include"
	// -- create and disable
	// late: hola, late! (debug=false)
	// greeter: active
	// shout: disabled
	// tenant: group
	// -- app.yml
	// # 应用的插件。
	// plugins:
	//   - id: greeter
	//     plugin: greeter
	//     config:
	//       greeting: goodbye # 运行期会被改掉
	//   - id: shout
	//     plugin: shout
	//     disabled: true
	//   # 这个分组有自己的 greeter，组内的 shout 绑定它。
	//   - id: tenant
	//     isolate:
	//       greeter: true
	//     plugins:
	//       - plugin: greeter
	//         config:
	//           greeting: hola
	//       - id: tenant-shout
	//         plugin: shout
	//         settings:
	//           logger: DEBUG
	//       - id: late
	//         plugin: shout
}
