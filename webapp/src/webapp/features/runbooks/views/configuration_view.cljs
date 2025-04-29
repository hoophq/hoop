(ns webapp.features.runbooks.views.configuration-view
  (:require
   ["@radix-ui/themes" :refer [Box Grid Flex Text Button Heading]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        git-url (r/atom "")
        git-ssh-key (r/atom "")
        is-submitting (r/atom false)
        config-loaded (r/atom false)
        handle-save (fn []
                      (reset! is-submitting true)
                      (rf/dispatch
                       [:runbooks-plugin->git-config
                        {:git-url @git-url
                         :git-ssh-key @git-ssh-key}])
                      (js/setTimeout
                       #(reset! is-submitting false)
                       1000))]

    (rf/dispatch [:plugins->get-plugin-by-name "runbooks"])

    (fn []
      (let [plugin (:plugin @plugin-details)
            config (get-in plugin [:config :envvars])]

        (when (and (not @config-loaded) config)
          (reset! config-loaded true)
          (reset! git-url (:GIT_URL config))
          (reset! git-ssh-key (:GIT_SSH_KEY config)))

        [:> Box {:py "7" :class "space-y-radix-9"}
         [:> Grid {:columns "7" :gap "7"}
          [:> Box {:grid-column "span 2 / span 2"}
           [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
            "Git repository"]
           [:> Text {:size "3" :class "text-[--gray-11]"}
            "Here you will integrate with one repository to consume your runbooks."]]

          [:> Box {:class "space-y-radix-7" :grid-column "span 5 / span 5"}
           [forms/input {:label "Git URL"
                         :placeholder "e.g. email@github.com:company/repository.git"
                         :value @git-url
                         :on-change #(reset! git-url (-> % .-target .-value))
                         :class "w-full"}]

           [forms/textarea {:label "SSH Key"
                            :placeholder "Paste your SSH key"
                            :value @git-ssh-key
                            :on-change #(reset! git-ssh-key (-> % .-target .-value))
                            :class "w-full"
                            :multiline true
                            :rows 6}]]]

         [:> Flex {:justify "end" :class "w-full"}
          [:> Button {:size "3"
                      :loading @is-submitting
                      :disabled @is-submitting
                      :on-click handle-save}
           "Save"]]]))))
