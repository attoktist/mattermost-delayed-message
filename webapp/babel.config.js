// Browser targets match the Mattermost web app (starter template 2b7f511).
// preset-react runtime is automatic: JSX imports react/jsx-runtime, which
// webpack aliases to src/jsx_runtime.ts. No useBuiltIns — the host already
// ships a modern runtime, and core-js is not a dependency.

module.exports = {
    presets: [
        ['@babel/preset-env', {
            targets: {
                chrome: 110,
                firefox: 102,
                edge: 110,
                safari: '16.4',
            },
        }],
        ['@babel/preset-react', {
            runtime: 'automatic',
        }],
        ['@babel/preset-typescript', {
            allExtensions: true,
            isTSX: true,
        }],
    ],
};
