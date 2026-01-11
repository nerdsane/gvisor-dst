load("//tools:defs.bzl", "go_library", "go_test")

package(
    default_applicable_licenses = ["//:license"],
    licenses = ["notice"],
)

go_library(
    name = "dst",
    srcs = [
        "bloodhound.go",
        "control.go",
        "snapshot.go",
    ],
    visibility = ["//visibility:public"],
    deps = [
        "//pkg/log",
        "//pkg/sync",
    ],
)

go_test(
    name = "dst_test",
    srcs = [
        "bloodhound_test.go",
        "snapshot_test.go",
    ],
    library = ":dst",
)
