load("//tools:defs.bzl", "go_library", "go_test")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

go_library(
    name = "dst",
    srcs = [
        "dst.go",
        "endpoint.go",
    ],
    visibility = ["//visibility:public"],
    deps = [
        "//pkg/sync",
        "//pkg/tcpip",
        "//pkg/tcpip/header",
        "//pkg/tcpip/stack",
    ],
)

go_test(
    name = "dst_test",
    srcs = [
        "endpoint_test.go",
    ],
    library = ":dst",
    deps = [
        "//pkg/tcpip",
        "//pkg/tcpip/stack",
    ],
)
