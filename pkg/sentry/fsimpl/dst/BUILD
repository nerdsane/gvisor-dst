load("//tools:defs.bzl", "go_library", "go_test")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

go_library(
    name = "dst",
    srcs = [
        "dst.go",
    ],
    visibility = ["//visibility:public"],
    deps = [
        "//pkg/sentry/ktime",
        "//pkg/sync",
    ],
)

go_test(
    name = "dst_test",
    srcs = [
        "dst_test.go",
    ],
    library = ":dst",
)
