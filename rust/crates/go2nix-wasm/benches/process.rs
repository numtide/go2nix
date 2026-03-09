use criterion::{criterion_group, criterion_main, Criterion};
use go2nix_wasm::process;

const SMALL: &str = include_str!("small.toml");
const LARGE: &str = include_str!("app-full.toml");

fn bench_process(c: &mut Criterion) {
    c.bench_function("process_small (2 modules, 2 packages)", |b| {
        b.iter(|| process(SMALL).unwrap());
    });

    c.bench_function("process_large (478 modules, 3250 packages)", |b| {
        b.iter(|| process(LARGE).unwrap());
    });
}

criterion_group!(benches, bench_process);
criterion_main!(benches);
