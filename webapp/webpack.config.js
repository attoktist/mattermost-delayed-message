const path = require('path');

const PLUGIN_ID = require('../plugin.json').id;

const config = {
    entry: './src/index.tsx',
    resolve: {
        modules: [
            'src',
            'node_modules',
        ],
        extensions: ['.js', '.jsx', '.ts', '.tsx'],
        alias: {
            'react/jsx-runtime$': path.resolve(__dirname, 'src/jsx_runtime.ts'),
            'react/jsx-dev-runtime$': path.resolve(__dirname, 'src/jsx_runtime.ts'),
        },
    },
    module: {
        rules: [
            {
                test: /\.(js|jsx|ts|tsx)$/,
                exclude: /node_modules/,
                use: {
                    loader: 'babel-loader',
                    options: {
                        cacheDirectory: true,
                    },
                },
            },
        ],
    },

    // Host globals. React stays out of the bundle; JSX goes through the alias above.
    externals: {
        react: 'React',
        'react-dom': 'ReactDOM',
        redux: 'Redux',
        'react-redux': 'ReactRedux',
    },
    output: {
        devtoolNamespace: PLUGIN_ID,
        path: path.join(__dirname, 'dist'),
        publicPath: '/',
        filename: 'main.js',
    },
};

module.exports = config;
