(ns webapp.agents.deployment
  (:require
    [re-frame.core :as rf]
    ["@radix-ui/themes" :refer [Grid Flex Box Text
                                Table Heading Code]]
    ["lucide-react" :refer [Copy]]
    [webapp.config :as config]
    [webapp.components.button :as button]))

(defmulti installation identity)
(defmethod installation "Kubernetes" [_]
  [:> Flex {:direction "column" :gap "4"}
   [:> Box "Minimal configuration"
    [:div "values.yml"]]
   [:> Box "standalone deployment"
    [:div "Helm"]
    [:div "deployment.yml"]]])

(defmethod installation "Docker Hub" [_]
  [:> Flex {:direction "column" :gap "6"}
   ; docker image repository
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
        [:> Table.ColumnHeaderCell "value"]
        ]]
      [:> Table.Body
       [:> Table.Row
        [:> Table.RowHeaderCell
         [:> Flex {:gap "4" :align "center"}
          [:> Text "HOOP_KEY"]
          [:> Box {:on-click (fn []
                               (js/navigator.clipboard.writeText "HOOP_KEY")
                               (rf/dispatch [:show-snackbar {:level :success
                                                             :text "Copied to clipboard"}]))
                   :class "cursor-pointer"}
           [:> Copy {:size 14 :color "gray"}]]]]
        [:> Table.Cell
         [:> Flex {:gap "4" :align "center"}
          [:> Text "your-keyaaisudhfiaushdfisahd-asdfasdfsa-d-fasdasdfasf-asdfasfasf-asfsafd-as-f-asf-as-fsdfa"]
          [:> Box {:on-click (fn []
                               (js/navigator.clipboard.writeText "agent key")
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
     [:> Box
       [:> Code "docker container run hoophq/hoopdev:latest \\"]]]]])

(defn main
  "function that render the instructions for each deployment method
  installation-method -> 'Docker Hub' | 'Kubernetes'"
  [{:keys [installation-method]}]
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
       :href "https://hoop.dev/docs/concepts/agent"}]]
    [:> Box {:class "space-y-radix-7" :gridColumn "span 5 / span 5"}
     [installation installation-method]]]])
