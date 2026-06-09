export default function(ctx: any, options: any) {
  const token = options.token;
  if (!token) return;

  const basic = gw.crypto.base64.encode("x-access-token:" + token);
  ctx.request.setHeader("Authorization", "Basic " + basic);
  gw.secrets.register(token);
}
