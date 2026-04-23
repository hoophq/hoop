(ns webapp.features.ai-session-analyzer.views.configuration-view
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading Text]]
   ["lucide-react" :refer [PencilRuler]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.callout-link :as callout-link]
   [webapp.components.forms :as forms]
   [webapp.components.selection-card :as selection-card]))

(def providers
  [{:id "azure-openai"
    :label "Azure OpenAI"
    :logo "/images/azure-logo.svg"}
   {:id "anthropic"
    :label "Anthropic"
    :logo "/images/anthropic-logo.svg"}
   {:id "openai"
    :label "OpenAI"
    :logo "/images/openai-logo.svg"}
   {:id "custom"
    :label "Custom"
    :logo nil}])

(def provider-model-docs
  {"anthropic" "https://platform.claude.com/docs/en/about-claude/models/overview"
   "azure-openai" "https://ai.azure.com/catalog/models"
   "openai" "https://developers.openai.com/api/docs/models"})

(defn- icon-component [logo selected?]
  (if logo
    (r/as-element
     [:figure {:class "w-5 h-5 flex items-center justify-center"}
      [:img {:class (str "w-5 h-5 object-contain"
                         (when selected? " brightness-0 invert"))
             :src logo}]])
    (r/as-element [:> PencilRuler {:size 16}])))


(defn main [active-tab]
  (let [config-data (rf/subscribe [:ai-session-analyzer/provider])
        config-loaded (r/atom false)
        is-submitting (r/atom false)
        provider-states (r/atom {})

        form-state (r/atom {:provider "openai"
                            :model ""
                            :api-key ""
                            :api-url ""})
        
        handle-provider-change (fn [provider-id]
                                 (let [{:keys [provider model api-key api-url]} @form-state]
                                   (swap! provider-states assoc provider {:model model :api-key api-key :api-url api-url}))
                                 (let [saved (get @provider-states provider-id {})]
                                   (swap! form-state assoc
                                          :provider provider-id
                                          :model    (or (:model saved) "")
                                          :api-key  (or (:api-key saved) "")
                                          :api-url  (or (:api-url saved) ""))))

        handle-save (fn []
                      (let [{:keys [provider model api-key api-url]} @form-state]
                        (cond
                          (str/blank? model)
                          (rf/dispatch [:show-snackbar {:level :error :text "Model is required."}])

                          (str/blank? api-key)
                          (rf/dispatch [:show-snackbar {:level :error :text "API Key is required."}])

                          (and (or (= provider "azure-openai") (= provider "custom"))
                               (str/blank? api-url))
                          (rf/dispatch [:show-snackbar {:level :error :text "API URL is required for this provider."}])

                          :else
                          (do
                            (reset! is-submitting true)
                            (let [requires-api-url? (or (= provider "azure-openai") (= provider "custom"))
                                  payload {:provider provider
                                           :model model
                                           :api_key api-key
                                           :api_url (if requires-api-url? api-url "")}
                                  on-success (fn []
                                               (rf/dispatch [:show-snackbar {:level :success :text "Configuration saved."}])
                                               (reset! is-submitting false)
                                               (reset! active-tab "rules"))
                                  on-failure (fn [_]
                                               (reset! is-submitting false))]
                              (rf/dispatch [:ai-session-analyzer/upsert-provider payload on-success on-failure]))))))]

    (fn []
      (let [config-status (:status @config-data)
            config-response (:data @config-data)
            loaded {:provider (or (:provider config-response) "openai")
                    :model    (or (:model config-response) "")
                    :api-key  (or (:api_key config-response) "")
                    :api-url  (or (:api_url config-response) "")}]

        (when (and (not @config-loaded)
                   (or (= config-status :success)
                       (= config-status :error)))
          (reset! config-loaded true)
          (when (and (= config-status :success) config-response)
            (reset! form-state loaded)
            (swap! provider-states assoc (:provider loaded) (dissoc loaded :provider))))

        (let [{:keys [provider model api-key api-url]} @form-state]
          [:> Box {:pb "7" :class "space-y-radix-7"}

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              "Select your provider"]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Select between a market or custom model. Custom models need to follow the OpenAI API pattern."]]

            [:> Box {:grid-column "span 5 / span 5" :class "max-w-[600px]"}
             [:> Grid {:class "grid-cols-[repeat(auto-fill,minmax(160px,1fr))] gap-3"}
              (for [p providers]
                ^{:key (:id p)}
                [selection-card/selection-card
                 {:icon (icon-component (:logo p) (= provider (:id p)))
                  :title (:label p)
                  :selected? (= provider (:id p))
                  :on-click #(handle-provider-change (:id p))}])]]]

           [:> Grid {:columns "7" :gap "7"}
            [:> Box {:grid-column "span 2 / span 2"}
             [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
              (str (-> (filter #(= (:id %) provider) providers) first :label) " configuration")]
             [:> Text {:size "3" :class "text-[--gray-11]"}
              "Set your provider model and API key to enable AI features."]
             (when-let [href (get provider-model-docs provider)]
               [callout-link/main {:href href
                                   :text "See supported AI models"}])]

            [:> Box {:grid-column "span 5 / span 5" :class "space-y-radix-4 max-w-[600px]"}
             (when (or (= provider "azure-openai") (= provider "custom"))
               [forms/input
                {:label "API URL"
                 :placeholder "https://your-endpoint.openai.azure.com/"
                 :value api-url
                 :on-change #(swap! form-state assoc :api-url (-> % .-target .-value))}])

             [forms/input
              {:label "Model"
               :placeholder "Enter model name (e.g. gpt-4o)"
               :value model
               :on-change #(swap! form-state assoc :model (-> % .-target .-value))}]

             [forms/input
              {:label "API Key"
               :type "password"
               :placeholder "Insert your API Key"
               :value api-key
               :on-change #(swap! form-state assoc :api-key (-> % .-target .-value))}]

             [:> Flex {:justify "start"}
              [:> Button {:size "3"
                          :loading @is-submitting
                          :disabled @is-submitting
                          :on-click handle-save}
               "Save"]]]]])))))