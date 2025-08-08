# Use a single-stage build for development, leveraging Vite's dev server and proxy
FROM node:20-alpine
WORKDIR /app
COPY package*.json ./
# Work around registry issues by pruning problematic deps and lock drift before install
RUN node -e "const fs=require('fs');const p=JSON.parse(fs.readFileSync('package.json','utf8'));p.dependencies=p.dependencies||{};delete p.dependencies['@codemirror/lang-tex'];fs.writeFileSync('package.json',JSON.stringify(p,null,2)); if(fs.existsSync('package-lock.json')) fs.unlinkSync('package-lock.json');"
RUN npm install --no-audit --no-fund
COPY . .
EXPOSE 3000
# Run the Vite dev server, which will use the proxy configuration.
CMD ["npm", "run", "dev"]