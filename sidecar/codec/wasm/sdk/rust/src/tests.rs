use std::collections::BTreeMap;

use serde_json::{json, Value};

use crate::abi;
use crate::*;

/// A codec that echoes each byte as one statement, so the dispatch tests
/// can tell connections and calls apart.
#[derive(Default)]
struct Echo {
    prefix: String,
    rewriting: bool,
}

impl Codec for Echo {
    fn open(&mut self, options: &BTreeMap<String, String>) -> Result<(), String> {
        self.prefix = options.get("prefix").cloned().unwrap_or_default();
        if self.prefix == "refuse" {
            return Err("refused".into());
        }
        Ok(())
    }

    fn decode(&mut self, _dir: Direction, data: &[u8]) -> Result<Decoded, String> {
        if data.first() == Some(&b'!') {
            return Err("bang".into());
        }
        let statements = data
            .iter()
            .map(|b| Statement::new(Operation::Other, format!("{}{}", self.prefix, *b as char)))
            .collect();
        Ok(Decoded { statements, consumed: data.len() })
    }

    fn deny(&self, _dir: Direction, message: &str) -> Vec<u8> {
        format!("{}:{message}", self.prefix).into_bytes()
    }

    fn enable_rewrite(&mut self) {
        self.rewriting = true;
    }

    fn take_credential(&mut self, mut stmt: Statement) -> Option<(String, Statement)> {
        let token = stmt.metadata.remove("token")?;
        Some((token, stmt))
    }

    fn content(&self, stmt: &Statement) -> Option<Content> {
        if stmt.text.is_empty() {
            return None;
        }
        Some(Content { text: stmt.text.clone(), cache_key: "k".into() })
    }
}

fn parse(bytes: Vec<u8>) -> Value {
    serde_json::from_slice(&bytes).expect("export output is JSON")
}

#[test]
fn packed_return_round_trips_and_zero_is_no_output() {
    assert_eq!(abi::pack(0x1000, 7), 0x0000_1000_0000_0007);
    assert_eq!(abi::unpack(0x0000_1000_0000_0007), (0x1000, 7));
    assert_eq!(abi::unpack(abi::pack(u32::MAX, u32::MAX)), (u32::MAX, u32::MAX));
    assert_eq!(abi::pack(0, 0), 0);
}

#[test]
fn base64_is_standard_with_padding() {
    for (raw, want) in [
        (&b""[..], ""),
        (b"f", "Zg=="),
        (b"fo", "Zm8="),
        (b"foo", "Zm9v"),
        (b"foob", "Zm9vYg=="),
        (b"\xff\xfe\xfd", "//79"),
    ] {
        assert_eq!(abi::base64(raw), want, "{raw:?}");
    }
}

#[test]
fn statement_json_uses_the_abi_names_and_omits_empty_fields() {
    let stmt = Statement::new(Operation::Delete, "PURGE orders")
        .with_metadata("x-acmewire.verb", "PURGE")
        .with_result(ResultDetail { columns: vec![Column::named("id")], row_count: 3, truncated: false });
    let mut stmt = stmt;
    stmt.effects = vec![Operation::Delete];
    stmt.relations = vec![Relation { name: "orders".into(), access: Access::Write }];
    stmt.tables = vec!["orders".into()];
    assert_eq!(
        serde_json::to_value(&stmt).unwrap(),
        json!({
            "text": "PURGE orders",
            "operation": "delete",
            "effects": ["delete"],
            "relations": [{"name": "orders", "access": "write"}],
            "tables": ["orders"],
            "result": {"columns": [{"name": "id"}], "row_count": 3},
            "metadata": {"x-acmewire.verb": "PURGE"}
        })
    );
    // The minimum: no direction (the host fills it), nothing empty.
    assert_eq!(
        serde_json::to_value(Statement::new(Operation::Other, "AUTH")).unwrap(),
        json!({"text": "AUTH", "operation": "other"})
    );
}

#[test]
fn statement_json_from_the_host_parses_with_protocol_and_direction_ignored_or_kept() {
    // What the host hands take_credential/content: its own Statement,
    // with direction set. It strips protocol, but an older host that
    // leaves it in must not break the guest.
    let raw = json!({
        "protocol": "x-acmewire", "direction": "server", "text": "ROW", "operation": "other",
        "result": {"row_count": 1, "truncated": true, "columns": [{"name": "id", "data_type_oid": 23}]},
        "database": "shop"
    });
    let stmt: Statement = serde_json::from_value(raw).unwrap();
    assert_eq!(stmt.direction, Some(Direction::Server));
    assert_eq!(stmt.database, "shop");
    let result = stmt.result.unwrap();
    assert!(result.truncated);
    assert_eq!(result.columns[0].data_type_oid, 23);
}

#[test]
fn every_operation_the_host_lists_round_trips() {
    // The list in ABI.md, which is inspect.Operations(). A value missing
    // here is a statement the host refuses.
    let names = [
        "select", "insert", "update", "delete", "create", "drop", "alter", "truncate", "grant", "revoke", "call",
        "show", "set", "begin", "commit", "rollback", "get", "post", "put", "patch", "head", "options", "connect",
        "trace", "exec_line", "env_set", "sftp_read", "sftp_write", "sftp_remove", "sftp_rename", "sftp_mkdir",
        "sftp_rmdir", "sftp_list", "sftp_stat", "sftp_setstat", "sftp_symlink", "ws_message", "ws_close", "other",
        "unknown",
    ];
    for name in names {
        let op: Operation = serde_json::from_value(Value::String(name.into())).unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(serde_json::to_value(op).unwrap(), Value::String(name.into()));
    }
    assert!(serde_json::from_value::<Operation>(Value::String("purge".into())).is_err());
    assert_eq!(Operation::default(), Operation::Unknown, "a forgotten verb fails closed");
}

#[test]
fn sql_analysis_parses_the_host_payload_and_copies_onto_a_statement() {
    let analysis: SqlAnalysis = serde_json::from_value(json!({
        "operation": "select", "effects": ["select"],
        "relations": [{"name": "orders", "access": "read"}], "tables": ["orders"],
        "complete": true, "reason": ""
    }))
    .unwrap();
    let stmt = Statement::new(Operation::Unknown, "SELECT 1 FROM orders").with_analysis(analysis);
    assert_eq!(stmt.operation, Operation::Select);
    assert_eq!(stmt.tables, vec!["orders"]);
    assert!(stmt.metadata.is_empty());

    // Arrays the host omitted (omitempty) parse as empty; an incomplete
    // scan fails closed and says why under the key libhoop uses.
    let incomplete: SqlAnalysis =
        serde_json::from_value(json!({"operation": "unknown", "complete": false, "reason": "unterminated string"}))
            .unwrap();
    let stmt = Statement::new(Operation::Select, "SELECT '").with_analysis(incomplete);
    assert_eq!(stmt.operation, Operation::Unknown);
    assert_eq!(stmt.metadata["sql.incomplete"], "unterminated string");
}

#[test]
fn manifest_json_matches_the_abi_example() {
    let mut m = Manifest::new("x-acmewire", "Acme Wire");
    m.version = "1.3.0".into();
    m.capabilities = vec![Capability::Deny, Capability::Rewrite];
    m.sql_dialect = Some(SqlDialect::Mysql);
    m.max_reassembly = Some(8388608);
    m.instances = Some(Instances::PerConnection);
    m.call_timeout_ms = Some(2000);
    m.memory_limit_pages = Some(1024);
    m.options = vec![OptionSpec {
        name: "compat_mode".into(),
        label: Some("Compat mode".into()),
        kind: OptionType::Enum,
        values: vec!["v3".into(), "v4".into()],
        default: Some("v4".into()),
        help: Some("...".into()),
    }];
    assert_eq!(
        serde_json::to_value(&m).unwrap(),
        json!({
            "abi": 1,
            "protocol": "x-acmewire",
            "label": "Acme Wire",
            "version": "1.3.0",
            "capabilities": ["deny", "rewrite"],
            "sql_dialect": "mysql",
            "max_reassembly": 8388608,
            "instances": "per_connection",
            "call_timeout_ms": 2000,
            "memory_limit_pages": 1024,
            "options": [
                {"name": "compat_mode", "label": "Compat mode", "type": "enum",
                 "values": ["v3", "v4"], "default": "v4", "help": "..."}
            ]
        })
    );
    assert_eq!(
        serde_json::to_value(Manifest::new("x-min", "Min")).unwrap(),
        json!({"abi": 1, "protocol": "x-min", "label": "Min"})
    );
}

#[test]
fn describe_holds_the_manifest_to_the_exported_capabilities() {
    let mut m = Manifest::new("x-t", "T");
    m.capabilities = vec![Capability::Rewrite, Capability::Deny];
    let out = parse(describe(&m, &[Capability::Deny, Capability::Rewrite]));
    assert_eq!(out["capabilities"], json!(["rewrite", "deny"]));

    let r = std::panic::catch_unwind(|| describe(&m, &[Capability::Deny]));
    assert!(r.is_err(), "a declared capability without its export must trap");
    let mut other = Manifest::new("x-t", "T");
    other.abi = 2;
    assert!(std::panic::catch_unwind(|| describe(&other, &[])).is_err());
}

#[test]
fn connections_dispatch_by_conn_and_keep_state_apart() {
    let conns: Connections<Echo> = Connections::new();
    assert_eq!(conns.open(1, br#"{"prefix": "a"}"#), 0);
    assert_eq!(conns.open(2, b""), 0);
    assert_eq!(conns.open(3, br#"{"prefix": "refuse"}"#), 2);
    assert_eq!(conns.open(4, b"[]"), 1, "options must be a string map");
    assert_eq!(conns.len(), 2);

    let one = parse(conns.decode(1, 0, b"xy"));
    assert_eq!(one["consumed"], 2);
    assert_eq!(one["statements"][0]["text"], "ax");
    assert_eq!(one["statements"][1]["text"], "ay");
    let two = parse(conns.decode(2, 1, b"z"));
    assert_eq!(two["statements"][0]["text"], "z");

    let err = parse(conns.decode(1, 0, b"!"));
    assert_eq!(err["error"], "bang");
    assert!(err.get("statements").is_none(), "an error result carries nothing else");

    let unopened = parse(conns.decode(9, 0, b"x"));
    assert!(unopened["error"].as_str().unwrap().contains("not opened"));

    assert_eq!(conns.deny(1, 0, b"no"), b"a:no");
    conns.close(1);
    assert_eq!(conns.len(), 1);
    let trapped = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| conns.deny(1, 0, b"no")));
    assert!(trapped.is_err(), "a closed connection traps");
    conns.close(1);
}

#[test]
fn capability_exports_render_their_result_json() {
    let conns: Connections<Echo> = Connections::new();
    conns.open(0, b"{}");

    let stmt = json!({"direction": "client", "text": "AUTH", "operation": "other", "metadata": {"token": "s3", "keep": "1"}});
    let lifted = parse(conns.take_credential(0, stmt.to_string().as_bytes()));
    assert_eq!(lifted["ok"], true);
    assert_eq!(lifted["credential"], "s3");
    assert_eq!(lifted["statement"]["metadata"], json!({"keep": "1"}));
    assert!(lifted["statement"].get("protocol").is_none());

    let none = parse(conns.take_credential(0, br#"{"text": "x", "operation": "other"}"#));
    assert_eq!(none, json!({"ok": false}));

    let content = parse(conns.content(0, br#"{"text": "SELECT 1", "operation": "select"}"#));
    assert_eq!(content, json!({"ok": true, "text": "SELECT 1", "cache_key": "k"}));
    let nothing = parse(conns.content(0, br#"{"text": "", "operation": "other"}"#));
    assert_eq!(nothing, json!({"ok": false}));

    // The default filter is the identity; the default rewrite forwards
    // untouched and counts nothing.
    assert_eq!(conns.filter(0, 1, b"abc"), b"abc");
    conns.enable_rewrite(0);
    let rewritten = parse(conns.rewrite(0, b"abc"));
    assert_eq!(rewritten, json!({"bytes": "YWJj", "cells": 0, "rows": 0}));
    assert_eq!(parse(conns.flush(0)), json!({"bytes": "", "cells": 0, "rows": 0}));
}

#[test]
fn direction_from_abi_rejects_anything_but_zero_and_one() {
    assert_eq!(Direction::from_abi(0), Direction::Client);
    assert_eq!(Direction::from_abi(1), Direction::Server);
    assert!(std::panic::catch_unwind(|| Direction::from_abi(2)).is_err());
}

// The macro must expand on the host target too, where it emits plain
// functions, so a plug-in crate can `cargo test` without a wasm runtime.
mod expands {
    #[derive(Default)]
    pub struct Nop;
    impl crate::Codec for Nop {
        fn decode(&mut self, _: crate::Direction, data: &[u8]) -> Result<crate::Decoded, String> {
            Ok(crate::Decoded { statements: vec![], consumed: data.len() })
        }
    }
    fn manifest() -> crate::Manifest {
        let mut m = crate::Manifest::new("x-nop", "Nop");
        m.capabilities = vec![crate::Capability::Deny, crate::Capability::Content];
        m
    }
    crate::export_codec!(Nop, manifest, [deny, content]);

    #[test]
    fn exports_exist_for_the_listed_capabilities_only() {
        // Names only: the pointer-taking bodies are wasm32's. What this
        // pins is that the macro accepts a subset and emits each wrapper.
        let _: extern "C" fn(u32, u32, u32, u32) -> u64 = __hoop_deny;
        let _: extern "C" fn(u32, u32, u32) -> u64 = __hoop_content;
        let _: extern "C" fn() -> u64 = __hoop_describe;
        let _: extern "C" fn(u32, u32, u32, u32) -> u64 = __hoop_decode;
        let _: extern "C" fn(u32) -> u32 = __hoop_alloc;
        let _: extern "C" fn(u32, u32) = __hoop_free;
        let _: extern "C" fn() = __hoop_initialize;
    }
}
