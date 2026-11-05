// Wails v3 generates plain-JS bindings (no .d.ts). Declaring the modules as
// any keeps the generated layer out of type-check scope; the call sites
// annotate their own shapes.
declare module '*/bindings/github.com/duihao/duihao/app';
declare module '*/bindings/github.com/duihao/duihao/backend/store/models';
declare module '*/bindings/github.com/duihao/duihao/backend/sync/models';
declare module '*/bindings/github.com/duihao/duihao/backend/duihao/service';
declare module '*/bindings/github.com/duihao/duihao/backend/duihao/service/*';
declare module '*/bindings/github.com/duihao/duihao/backend/duihao/service/models';
declare module '*/bindings/github.com/duihao/duihao/backend/duihao/instance/models';
