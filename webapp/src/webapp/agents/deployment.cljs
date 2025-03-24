(ns webapp.agents.deployment
  (:require
   [re-frame.core :as rf]
   ["@radix-ui/themes" :refer [Grid Flex Box Text
                               Table Heading Link]]
   ["lucide-react" :refer [Copy]]
   [webapp.config :as config]
   [webapp.components.button :as button]
   [webapp.components.code-snippet :as code-snippet]))

(defn values-yml [hoop-key]
  (str "config:\n"
       "  HOOP_KEY: " hoop-key "\n"
       "image:\n"
       "  repository: hoophq/hoopdev\n"
       "  tag: latest\n"))

(defn installing-helm [hoop-key]
  (str "VERSION=$(curl -s https://releases.hoop.dev/release/latest.txt)\n"
       "helm template hoopagent \\\n"
       "https://releases.hoop.dev/release/$VERSION/hoopagent-chart-$VERSION.tgz \\\n"
       "--set 'config.HOOP_KEY=" hoop-key "' \\\n"
       "--set 'image.tag=1.25.2' \\\n"
       "--set 'extraSecret=AWS_REGION=us-east-1' \\\n"))

(defn deployment-yml [hoop-key]
  (str "apiVersion: apps/v1\n"
       "kind: Deployment\n"
       "metadata:\n"
       "  name: hoopagent\n"
       "spec:\n"
       "  replicas: 1\n"
       "  selector:\n"
       "    matchLabels:\n"
       "      app: hoopagent\n"
       "  template:\n"
       "    metadata:\n"
       "      labels:\n"
       "        app: hoopagent\n"
       "    spec:\n"
       "      containers:\n"
       "      - name: hoopagent\n"
       "        image: hoophq/hoopdev\n"
       "        env:\n"
       "        - name: HOOP_KEY\n"
       "          value: '" hoop-key "'\n"))

(defmulti installation identity)
(defmethod installation "Kubernetes" [_ hoop-key]
  [:> Flex {:direction "column" :gap "6"}
   [:> Box
    [:> Flex {:direction "column" :gap "5"}

     [:> Flex {:direction "column" :gap "2"}
      [:> Text {:size "2" :weight "bold"}
       "Minimal configuration"]
      [:> Text {:size "1" :color "gray"}
       "Include the following parameters for standard installation."]]
     [:> Flex {:direction "column" :gap "5"}
      [:> Text {:size "2" :weight "bold"}
       "values.yml"]
      [code-snippet/main
       {:code (values-yml hoop-key)}]]]]
   [:> Flex {:direction "column" :gap "5"}
    [:> Text {:size "2" :weight "bold"}
     "Standalone deployment"]
    [:> Flex {:direction "column" :gap "2"}
     [:> Text {:size "2" :weight "bold"}
      "Helm"]
     [:> Text {:size "1" :color "gray"}
      "Make sure you have Helm installed. Check the "
      [:> Link {:href "https://helm.sh/docs/intro/install/"
                :target "_blank"}
       "Helm installation guide"]]]
    [code-snippet/main
     {:code (installing-helm hoop-key)}]]
   [:> Flex {:direction "column" :gap "5"}
    [:> Text {:size "2" :weight "bold"}
     "deployment.yml"]
    [code-snippet/main
     {:code (deployment-yml hoop-key)}]]])

(defmethod installation "Docker Hub" [_ hoop-key]
  [:> Flex {:direction "column" :gap "6"}
   [:> Box
    [:> Flex {:direction "column" :gap "4"}
     [:> Text {:size "2" :weight "bold"}
      "Docker image repository"]
     [:> Box {:p "3"
              :class "border border-[--gray-a6] rounded-xl"}
      [:> Flex {:gap "2" :align "center"}
       [:img {:src (str config/webapp-url "/images/docker-blue.svg")}]
       [:> Text {:size "1"}
        "hoophq/hoopdev:latest"]
       [:> Box {:ml "2"
                :on-click (fn []
                            (js/navigator.clipboard.writeText "hoophq/hoopdev:latest")
                            (rf/dispatch [:show-snackbar {:level :success
                                                          :text "Copied to clipboard"}]))
                :class "cursor-pointer"}
        [:> Copy {:size 14 :color "gray"}]]]]]]
   [:> Box
    [:> Flex {:direction "column" :gap "4"}
     [:> Text {:size "2" :weight "bold"}
      "Environment variables"]
     [:> Table.Root {:variant "surface"}
      [:> Table.Header
       [:> Table.Row
        [:> Table.ColumnHeaderCell "env-var"]
        [:> Table.ColumnHeaderCell "value"]]]
      [:> Table.Body
       [:> Table.Row
        [:> Table.RowHeaderCell
         [:> Flex {:gap "4"
                   :align "center"
                   :justify "center"
                   :height "100%"}
          [:> Text "HOOP_KEY"]
          [:> Box {:on-click (fn []
                               (js/navigator.clipboard.writeText "HOOP_KEY")
                               (rf/dispatch [:show-snackbar {:level :success
                                                             :text "Copied to clipboard"}]))
                   :class "cursor-pointer"}
           [:> Copy {:size 14 :color "gray"}]]]]
        [:> Table.Cell
         [:> Flex {:gap "4" :align "center"}
          [:> Text hoop-key]
          [:> Box {:on-click (fn []
                               (js/navigator.clipboard.writeText hoop-key)
                               (rf/dispatch [:show-snackbar {:level :success
                                                             :text "Copied to clipboard"}]))
                   :class "cursor-pointer"}
           [:> Copy {:size 14 :color "gray"}]]]]]]]]]
   [:> Box
    [:> Flex {:direction "column" :gap "4"}
     [:> Flex {:direction "column" :gap "2"}
      [:> Text {:size "2" :weight "bold"}
       "Manually running in a Docker container"]
      [:> Text {:size "1" :color "gray"}
       "If preferred, it is also possible to configure it manually with the following command."]]
     [code-snippet/main
      {:code (str "docker container run \\\n"
                  "-e HOOP_KEY='" hoop-key "' \\\n"
                  "--rm -d hoophq/hoopdev")}]]]])

(defn main
  "function that render the instructions for each deployment method
  installation-method -> 'Docker Hub' | 'Kubernetes'
  hoop-key -> the key for the agent. A.K.A: HOOP_KEY"
  [{:keys [installation-method hoop-key]}]
  [:div
   [:> Grid {:columns "7" :gap "7"}
    [:> Box {:gridColumn "span 2 / span 2"}
     [:> Flex {:direction "column"}
      [:> Heading {:size "4" :weight "medium" :as "h3"}
       "Agent deployment"]
      [:p {:class "text-sm text-gray-500"}
       "Setup your Agent in your infrastructure."]]
     [button/DocsBtnCallOut
      {:text "Learn more about Agents"
       :href (get-in config/docs-url [:concepts :agents])}]]
    [:> Box {:class "space-y-radix-7"
             :mb "8"
             :gridColumn "span 5 / span 5"}
     [installation installation-method hoop-key]]]])
