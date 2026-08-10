// @target: es2018

const thing = { nested: { condition: true } };

const wat = () =>
    // explanatory comment
    thing?.nested?.condition ? "pass" : "fail";

const watInline = () => /* inline explanatory comment */ thing?.nested?.condition ? "pass" : "fail";

declare const o: { a?: number[] };

export const f = () =>
    // comment
    o.a?.length;
