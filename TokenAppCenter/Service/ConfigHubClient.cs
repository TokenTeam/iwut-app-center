using TokenAppCenter.Config;
using TokenAppCenter.Model;

namespace TokenAppCenter.Service;

public class ConfigHubClient
{
    private const string AppCenterConfigKey = "appcenter";

    public HttpClient Client { get; private set; }

    public ConfigHubClient(HttpClient client, IConfiguration configuration)
    {
        var config = configuration.GetSection("ConfHub").Get<ConfhubServiceConfig>();
        client.BaseAddress = config is null 
            ? throw new ArgumentException("confhub service url not set")
            : new Uri(config.ServiceUrl);

        Client = client;
    }

    public async Task<AppCenterConfigResp?> GetAppCenterConfig(string platform)
    {
        var req = new HttpRequestMessage
        {
            Method = HttpMethod.Get,
            RequestUri = new Uri($"/blob/{AppCenterConfigKey}", UriKind.Relative)
        };

        req.Headers.Add("iwut-platform", platform);


        var resp = await Client.SendAsync(req).ConfigureAwait(false);
        resp.EnsureSuccessStatusCode();

        return await resp.Content.ReadFromJsonAsync<AppCenterConfigResp>().ConfigureAwait(false);
    } 
}
