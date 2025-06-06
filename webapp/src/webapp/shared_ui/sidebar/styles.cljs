(ns webapp.shared-ui.sidebar.styles)

;; Cores e estilos base
(def colors
  {:bg-primary "#182449"
   :text-primary "text-white"
   :text-secondary "text-gray-300"
   :hover-bg "bg-white/5"})

;; Estilos de links
(def link-base "flex gap-x-3 rounded-md p-2 text-sm leading-6 font-semibold")

(def link-styles
  {:enabled (str "flex justify-between items-center group items-start " link-base)
   :disabled (str "flex justify-between items-center text-gray-300 cursor-not-allowed text-opacity-30 "
                  "group items-start " link-base)})

;; Função para estilos de hover/active
(defn hover-side-menu-link [uri-item current-route]
  (if (= uri-item current-route)
    "bg-white/5 text-white "
    "hover:bg-white/5 hover:text-white text-gray-300 "))

;; Estilos específicos de componentes
(def sidebar-container
  {:mobile "fixed inset-0 flex"
   :desktop "hidden lg:fixed lg:inset-y-0 lg:z-40 lg:flex lg:w-side-menu lg:flex-col lg:bg-[#182449]"
   :collapsed "hidden lg:fixed lg:inset-y-0 lg:left-0 lg:z-30 lg:block lg:w-[72px] lg:overflow-y-auto lg:bg-[#182449]"})

;; Estilos para ícones
(def icon-styles
  {:standard "h-6 w-6 shrink-0 text-white"
   :disabled "h-6 w-6 shrink-0 text-white opacity-30"})

;; Badges e indicadores
(def badge-upgrade "text-xs text-gray-200 py-1 px-2 border border-gray-200 rounded-md")

;; Transições
(def transitions
  {:mobile-enter "transition-opacity ease-linear duration-500"
   :mobile-enter-from "opacity-0"
   :mobile-enter-to "opacity-100"
   :mobile-leave "transition-opacity ease-linear duration-500"
   :mobile-leave-from "opacity-100"
   :mobile-leave-to "opacity-0"

   :slide-enter "transition ease-in-out duration-700 transform"
   :slide-enter-from "-translate-x-full"
   :slide-enter-to "translate-x-0"
   :slide-leave "transition ease-in-out duration-700 transform"
   :slide-leave-from "translate-x-0"
   :slide-leave-to "-translate-x-full"

   :fade-enter "transition-opacity duration-400 ease-in-out transform"
   :fade-enter-from "opacity-0"
   :fade-enter-to "opacity-100"
   :fade-leave "transition-opacity duration-400 ease-in-out transform"
   :fade-leave-from "opacity-100"
   :fade-leave-to "opacity-0"})
